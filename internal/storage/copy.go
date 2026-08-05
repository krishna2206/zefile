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
)

// MaxSyncCopyBytes bounds a copy performed inside a request.
//
// Above it, the operation belongs in the background job queue: a copy that
// outlives a proxy timeout leaves the client with no answer while the server
// keeps working, which is worse than a clear refusal. On a plain VPS disk this
// threshold copies in a few seconds, and on Linux the kernel does it without
// the bytes passing through this process at all.
const MaxSyncCopyBytes = 1 << 30 // 1 GiB

// ErrTooLarge means the operation exceeds what may be done inside a request.
var ErrTooLarge = errors.New("storage: too large for a synchronous operation")

// Copy duplicates a single regular file.
//
// Directories are refused. Copying a tree means walking an unbounded number of
// entries, which cannot honestly be done inside a request; that arrives with
// the job queue.
//
// The copy is written to a temporary file in the reserved uploads directory and
// renamed into place once complete, so an interrupted copy never leaves a
// partial file where callers can see it, and never overwrites an existing file
// with a truncated one.
func (l *Local) Copy(ctx context.Context, from, to Path) error {
	if from.IsRoot() || to.IsRoot() {
		return fmt.Errorf("%w: cannot copy the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpRead, from); err != nil {
		return err
	}
	if err := l.access(ctx, OpWrite, to); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}

	fromRel, info, err := l.resolveExisting(from)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%w: copying a directory needs the background job queue", ErrIsDir)
	}
	if !info.Mode().IsRegular() {
		// Symlinks, sockets, devices. Duplicating them has no obvious meaning
		// and every plausible answer is surprising to someone.
		return fmt.Errorf("%w: only regular files can be copied", ErrInvalidPath)
	}
	if info.Size() > MaxSyncCopyBytes {
		return fmt.Errorf("%w: %d bytes exceeds the %d byte limit", ErrTooLarge, info.Size(), MaxSyncCopyBytes)
	}

	toRel, err := l.resolveForCreate(to)
	if err != nil {
		return err
	}
	// Refuse rather than overwrite. A copy silently replacing a file is a data
	// loss the caller did not ask for; the interface offers to replace only
	// after saying so.
	if _, err := l.root.Lstat(toRel); err == nil {
		return ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return mapErr(err)
	}

	tmpRel, err := l.newTempPath()
	if err != nil {
		return err
	}
	defer func() { _ = l.root.Remove(tmpRel) }() // no-op once renamed away

	if err := l.copyInto(ctx, fromRel, tmpRel); err != nil {
		return err
	}
	return mapErr(l.root.Rename(tmpRel, toRel))
}

// CopyTree copies a file or an entire directory tree, with no size limit.
//
// It is the background counterpart to [Copy]: a tree or a very large file may
// take minutes, so this runs in the job worker rather than a request. progress,
// if set, is called with the bytes copied so far and the total to copy.
//
// The whole tree is assembled under the reserved uploads directory and renamed
// into place only once complete, so an interrupted copy never leaves a partial
// tree where callers can see it, and an existing destination is never
// overwritten.
func (l *Local) CopyTree(ctx context.Context, from, to Path, progress func(done, total int64)) error {
	if from.IsRoot() || to.IsRoot() {
		return fmt.Errorf("%w: cannot copy the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpRead, from); err != nil {
		return err
	}
	if err := l.access(ctx, OpWrite, to); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}

	fromRel, info, err := l.resolveExisting(from)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: only regular files and directories can be copied", ErrInvalidPath)
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

	total := l.treeBytes(fromRel, info)
	var copied int64
	report := func(n int64) {
		copied += n
		if progress != nil {
			progress(copied, total)
		}
	}

	tmpRel, err := l.newTempPath()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = l.root.RemoveAll(tmpRel)
		}
	}()

	if info.IsDir() {
		if err := l.copyDirInto(ctx, fromRel, tmpRel, report); err != nil {
			return err
		}
	} else {
		if err := l.copyInto(ctx, fromRel, tmpRel); err != nil {
			return err
		}
		report(info.Size())
	}

	if err := l.root.Rename(tmpRel, toRel); err != nil {
		return mapErr(err)
	}
	committed = true
	return nil
}

// treeBytes sums the regular-file bytes under a resolved path. It is
// best-effort: an entry that vanishes mid-walk is skipped, since the total only
// scales a progress fraction.
func (l *Local) treeBytes(rel string, info os.FileInfo) int64 {
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return info.Size()
		}
		return 0
	}
	dir, err := l.root.Open(rel)
	if err != nil {
		return 0
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		child := rel + "/" + e.Name()
		ci, err := l.root.Lstat(child)
		if err != nil {
			continue
		}
		total += l.treeBytes(child, ci)
	}
	return total
}

// copyDirInto creates dstRel and copies the contents of srcRel into it,
// descending recursively. Only directories and regular files are reproduced;
// symlinks, devices and the like are skipped, as single-file [Copy] refuses
// them too. Reserved directories exist only at the root, so a nested walk never
// meets them.
func (l *Local) copyDirInto(ctx context.Context, srcRel, dstRel string, report func(int64)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.root.Mkdir(dstRel, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return mapErr(err)
	}

	dir, err := l.root.Open(srcRel)
	if err != nil {
		return mapErr(err)
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return mapErr(err)
	}

	for _, e := range entries {
		src := srcRel + "/" + e.Name()
		dst := dstRel + "/" + e.Name()
		ci, err := l.root.Lstat(src)
		if err != nil {
			continue
		}
		switch {
		case ci.IsDir():
			if err := l.copyDirInto(ctx, src, dst, report); err != nil {
				return err
			}
		case ci.Mode().IsRegular():
			if err := l.copyInto(ctx, src, dst); err != nil {
				return err
			}
			report(ci.Size())
		}
	}
	return nil
}

// copyInto streams one file onto another, leaving the destination durable.
func (l *Local) copyInto(ctx context.Context, fromRel, tmpRel string) error {
	src, err := l.root.Open(fromRel)
	if err != nil {
		return mapErr(err)
	}
	defer src.Close()

	dst, err := l.root.OpenFile(tmpRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return mapErr(err)
	}
	defer dst.Close()

	// Both sides are *os.File, so on Linux this becomes copy_file_range and the
	// data never enters this process. The context wrapper would defeat that, so
	// cancellation is checked between chunks instead of on every read.
	if err := copyWithCancel(ctx, dst, src); err != nil {
		return err
	}

	// Without this, a host power loss can leave a file that exists at its final
	// name with unwritten contents — the rename being durable while the data is
	// not is the classic way to end up with a plausible-looking empty file.
	if err := dst.Sync(); err != nil {
		return mapErr(err)
	}
	return nil
}

// copyChunk bounds how much is copied before cancellation is checked again.
// Large enough that the kernel fast path is still worth taking, small enough
// that an abandoned request stops promptly.
const copyChunk = 32 << 20 // 32 MiB

func copyWithCancel(ctx context.Context, dst io.Writer, src io.Reader) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.CopyN(dst, src, copyChunk)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return mapErr(err)
		}
		if n == 0 {
			return nil
		}
	}
}

// newTempPath reserves a name inside the uploads directory, creating it if
// needed.
//
// Temporary files live there rather than beside their destination so that a
// copy in flight is never visible in a listing, and so that they are on the
// same filesystem as the data — which is what keeps the final rename instant
// instead of turning into a second copy.
func (l *Local) newTempPath() (string, error) {
	if err := l.root.Mkdir(UploadsDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", mapErr(err)
	}

	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("storage: name temporary file: %w", err)
	}
	return path.Join(UploadsDir, "copy-"+base64.RawURLEncoding.EncodeToString(raw)), nil
}
