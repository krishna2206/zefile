package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// Staged files are partial uploads accumulating in the reserved uploads
// directory until they are complete.
//
// They live inside the storage root rather than in a system temporary
// directory for one reason: the final step has to be a rename, and a rename is
// only instantaneous within a single filesystem. Staging elsewhere would turn
// the completion of a forty-gigabyte upload into a second forty-gigabyte copy.
//
// Being under the root also means they inherit its free space, so an upload
// that would fill the disk fails while it is still a partial file rather than
// after it has been published.

// ErrNoStagedFile means the staged file is unknown or has already been
// committed or discarded.
var ErrNoStagedFile = errors.New("storage: no such staged file")

// StageID identifies a partial upload on disk. It is opaque to callers and
// never appears in a URL: the upload token in the database does that, so a
// leaked identifier reveals nothing and grants nothing.
type StageID string

// NewStage creates an empty staged file and returns its identifier.
//
// Free space is checked here rather than only while writing, so a client
// learns that a transfer cannot succeed before spending an hour on it.
func (l *Local) NewStage(_ context.Context) (StageID, error) {
	if err := l.checkWritable(); err != nil {
		return "", err
	}
	if err := l.ensureStagingDir(); err != nil {
		return "", err
	}

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: name staged file: %w", err)
	}
	id := StageID(base64.RawURLEncoding.EncodeToString(raw))

	f, err := l.root.OpenFile(l.stagePath(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", mapErr(err)
	}
	if err := f.Close(); err != nil {
		return "", mapErr(err)
	}
	return id, nil
}

// EnsureStage opens a staged file under a caller-chosen identifier, creating it
// empty if it does not yet exist, and returns how many bytes it already holds.
//
// It backs resumable server-side work — a paused download names its stage after
// the job, so resuming finds the bytes already fetched and continues from the
// returned offset. Unlike [NewStage] the identifier is deterministic, so the
// same job always addresses the same partial file across runs.
func (l *Local) EnsureStage(ctx context.Context, id StageID) (int64, error) {
	if err := l.checkWritable(); err != nil {
		return 0, err
	}
	if err := l.ensureStagingDir(); err != nil {
		return 0, err
	}
	if size, err := l.StageSize(ctx, id); err == nil {
		return size, nil
	} else if !errors.Is(err, ErrNoStagedFile) {
		return 0, err
	}
	f, err := l.root.OpenFile(l.stagePath(id), os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return 0, mapErr(err)
	}
	if err := f.Close(); err != nil {
		return 0, mapErr(err)
	}
	return 0, nil
}

// StageSize reports how many bytes a staged file already holds, which is the
// offset a client resumes from.
func (l *Local) StageSize(_ context.Context, id StageID) (int64, error) {
	info, err := l.root.Lstat(l.stagePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrNoStagedFile
		}
		return 0, mapErr(err)
	}
	return info.Size(), nil
}

// AppendToStage writes at the given offset and returns the new size.
//
// The offset is honoured rather than assumed: a client resuming after a dropped
// connection may repeat bytes it already sent, and writing them again at the
// position they belong to is correct where appending blindly would corrupt the
// file.
func (l *Local) AppendToStage(ctx context.Context, id StageID, offset int64, src io.Reader) (int64, error) {
	if err := l.checkWritable(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, fmt.Errorf("%w: negative offset", ErrInvalidPath)
	}

	current, err := l.StageSize(ctx, id)
	if err != nil {
		return 0, err
	}
	if offset > current {
		// A gap would leave a hole of zeros that no checksum could catch,
		// producing a file that looks complete and is silently wrong.
		return 0, fmt.Errorf("%w: offset %d is beyond the %d bytes received", ErrInvalidPath, offset, current)
	}

	f, err := l.root.OpenFile(l.stagePath(id), os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrNoStagedFile
		}
		return 0, mapErr(err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, mapErr(err)
	}
	written, err := io.Copy(f, ctxAwareReader{ctx: ctx, r: src})
	if err != nil {
		// Whatever landed stays: that is the point of a resumable upload. The
		// client asks for the offset again and continues from there.
		return offset + written, err
	}

	if err := f.Sync(); err != nil {
		return 0, mapErr(err)
	}
	return max(current, offset+written), nil
}

// CommitStage moves a completed staged file to its destination.
//
// Authorisation runs here, at the moment the bytes become a file anyone can
// see, in addition to when the upload was opened. A permission withdrawn
// during a long transfer therefore takes effect.
func (l *Local) CommitStage(ctx context.Context, id StageID, to Path) error {
	if to.IsRoot() {
		return fmt.Errorf("%w: cannot write to the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpWrite, to); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}

	if _, err := l.StageSize(ctx, id); err != nil {
		return err
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

	if err := l.root.Rename(l.stagePath(id), toRel); err != nil {
		return mapErr(err)
	}
	// Staged files are private while they are partial; the finished file gets
	// the ordinary mode so it behaves like anything else in the tree.
	return mapErr(l.root.Chmod(toRel, 0o644))
}

// OpenStage opens a staged file for reading, so a checksum can be computed
// before the file is published.
func (l *Local) OpenStage(_ context.Context, id StageID) (File, error) {
	f, err := l.root.Open(l.stagePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoStagedFile
		}
		return nil, mapErr(err)
	}
	return f, nil
}

// DiscardStage deletes a staged file. It is idempotent: an upload abandoned
// twice is not an error.
func (l *Local) DiscardStage(_ context.Context, id StageID) error {
	err := l.root.Remove(l.stagePath(id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return mapErr(err)
	}
	return nil
}

// PurgeStagesOlderThan deletes staged files untouched since the cutoff.
//
// Abandoned uploads are the one thing in the tree that grows without anyone
// asking, and they are invisible in listings, so nothing else would ever
// prompt someone to clean them up.
func (l *Local) PurgeStagesOlderThan(_ context.Context, cutoff time.Time) (int, error) {
	dir, err := l.root.Open(UploadsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, mapErr(err)
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, mapErr(err)
	}

	removed := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue // vanished under us; nothing to purge
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := l.root.Remove(path.Join(UploadsDir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}

func (l *Local) ensureStagingDir() error {
	if err := l.root.Mkdir(UploadsDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return mapErr(err)
	}
	return nil
}

// stagePath is the on-disk location of a staged file.
//
// The identifier is generated here and never comes from a client, but it is
// still stripped of separators: a value that reaches a path join deserves the
// same treatment whatever its provenance, because provenance changes.
func (l *Local) stagePath(id StageID) string {
	clean := strings.NewReplacer("/", "", "\\", "", ".", "").Replace(string(id))
	return path.Join(UploadsDir, clean)
}

// ctxAwareReader stops a read when the request is abandoned.
type ctxAwareReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxAwareReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
