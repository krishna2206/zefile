package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

// TrashID names an entry parked in the reserved trash directory.
//
// Like a stage identifier it is generated here, never supplied by a client, and
// stripped of separators before it reaches a path join: a value that reaches a
// path deserves the same treatment whatever its provenance.
type TrashID string

func (l *Local) ensureTrashDir() error {
	if err := l.root.Mkdir(TrashDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return mapErr(err)
	}
	return nil
}

func (l *Local) trashPath(id TrashID) string {
	clean := strings.NewReplacer("/", "", "\\", "", ".", "").Replace(string(id))
	return path.Join(TrashDir, clean)
}

// Trash moves an entry into the reserved trash directory and returns the
// identifier it now lives under.
//
// It is how deletion is spelled once a trash exists, so it authorises deletion
// of the source. Moving into the trash is a rename, and restoring is a second
// one: the bytes of a forty-gigabyte file never move, only its name — the same
// reason uploads stage under the root rather than in a system temp directory.
func (l *Local) Trash(ctx context.Context, p Path) (TrashID, error) {
	if p.IsRoot() {
		return "", fmt.Errorf("%w: cannot remove the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpDelete, p); err != nil {
		return "", err
	}
	if err := l.checkDeletable(); err != nil {
		return "", err
	}

	fromRel, _, err := l.resolveExisting(p)
	if err != nil {
		return "", err
	}
	if err := l.ensureTrashDir(); err != nil {
		return "", err
	}

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: name trashed entry: %w", err)
	}
	id := TrashID(base64.RawURLEncoding.EncodeToString(raw))

	if err := l.root.Rename(fromRel, l.trashPath(id)); err != nil {
		return "", mapErr(err)
	}
	return id, nil
}

// Restore moves a trashed entry back to a destination path.
//
// The destination must not already exist: silently overwriting, or inventing a
// suffixed name, would each surprise someone who expected the file back exactly
// where it was. If the original parent directory is itself gone, the resolve
// fails and the caller learns the folder no longer exists.
func (l *Local) Restore(ctx context.Context, id TrashID, to Path) error {
	if to.IsRoot() {
		return fmt.Errorf("%w: cannot restore over the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpWrite, to); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}

	from := l.trashPath(id)
	if _, err := l.root.Lstat(from); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotExist
		}
		return mapErr(err)
	}

	toRel, err := l.resolveForCreate(to)
	if err != nil {
		return err
	}
	if _, err := l.root.Lstat(toRel); err == nil {
		return ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapErr(err)
	}

	return mapErr(l.root.Rename(from, toRel))
}

// PurgeTrash deletes a trashed entry for good.
//
// It is idempotent: purging one already gone is not an error, so emptying the
// trash never half-fails on a stale row.
func (l *Local) PurgeTrash(_ context.Context, id TrashID) error {
	if err := l.checkDeletable(); err != nil {
		return err
	}
	if err := l.root.RemoveAll(l.trashPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return mapErr(err)
	}
	return nil
}

// TrashStat reports the size and kind of a trashed entry, which the metadata
// table does not keep — the filesystem stays the source of truth for both.
func (l *Local) TrashStat(_ context.Context, id TrashID) (os.FileInfo, error) {
	info, err := l.root.Lstat(l.trashPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotExist
		}
		return nil, mapErr(err)
	}
	return info, nil
}
