package storage

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Extraction ceilings. They exist to refuse a decompression bomb — an archive a
// few kilobytes on disk that expands to fill a disk or exhaust an inode table.
//
// The numbers are generous for honest archives and ruinous for a bomb: a real
// backup or photo dump stays well under them, while the classic 42-kilobyte zip
// that unpacks to petabytes trips every one.
const (
	// MaxArchiveEntries bounds how many files and directories an archive may
	// contain. A bomb often carries millions of tiny entries to exhaust inodes
	// rather than bytes.
	MaxArchiveEntries = 100_000

	// MaxExtractedBytes bounds the total uncompressed size written to disk.
	MaxExtractedBytes = 20 << 30 // 20 GiB

	// MaxCompressionRatio bounds how far a single entry may expand. An honest
	// file compresses maybe 10× or 20×; a bomb expands thousands of times. The
	// ratio is checked against bytes actually written, not the header's claim,
	// so a lying header cannot slip past it.
	MaxCompressionRatio = 200

	// ratioFloorBytes is the size below which the ratio check does not apply. A
	// one-byte file legitimately sits in a zip block far larger than itself, so
	// enforcing a ratio on tiny entries would reject honest archives.
	ratioFloorBytes = 1 << 20 // 1 MiB
)

// ExtractZip unpacks a ZIP archive into a new directory beside it and returns
// the path of that directory.
//
// The destination is dest/<archive name without .zip>. It must not already
// exist: an extraction never merges into or overwrites existing files. The
// whole tree is assembled under the reserved uploads directory and renamed into
// place only once complete, so an interrupted extraction never leaves a partial
// tree where callers can see it — the same discipline as [CopyTree].
//
// Safety is layered. Traversal is handled by the storage layer itself: every
// entry name is rebuilt component by component through the same validation a
// path from a client goes through, so "../../etc/passwd" is refused before it
// reaches the disk, and os.Root refuses any symlink that resolves outside the
// tree. Decompression bombs are refused by the ceilings above, the ratio one
// enforced against bytes actually written rather than the header's claim.
// Symlinks and other non-regular entries are ignored rather than reproduced.
func (l *Local) ExtractZip(ctx context.Context, archive, dest Path, progress func(fraction float64)) (Path, error) {
	if err := l.access(ctx, OpRead, archive); err != nil {
		return Path{}, err
	}
	if err := l.access(ctx, OpWrite, dest); err != nil {
		return Path{}, err
	}
	if err := l.checkWritable(); err != nil {
		return Path{}, err
	}

	target, err := dest.Child(strings.TrimSuffix(archive.Name(), ".zip"))
	if err != nil {
		return Path{}, err
	}
	// A destination that would be the archive's own name with the suffix intact
	// (the archive was not a .zip, or sits at the destination) must not collide
	// with the archive itself.
	if target.String() == archive.String() {
		target, err = dest.Child(archive.Name() + ".extracted")
		if err != nil {
			return Path{}, err
		}
	}

	targetRel, err := l.resolveForCreate(target)
	if err != nil {
		return Path{}, err
	}
	if _, err := l.root.Lstat(targetRel); err == nil {
		return Path{}, ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return Path{}, mapErr(err)
	}

	archiveRel, info, err := l.resolveExisting(archive)
	if err != nil {
		return Path{}, err
	}
	if !info.Mode().IsRegular() {
		return Path{}, fmt.Errorf("%w: not a regular file", ErrBadArchive)
	}

	f, err := l.root.Open(archiveRel)
	if err != nil {
		return Path{}, mapErr(err)
	}
	defer f.Close()

	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return Path{}, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	if err := preflightZip(zr); err != nil {
		return Path{}, err
	}

	tmpRel, err := l.newTempDir()
	if err != nil {
		return Path{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = l.root.RemoveAll(tmpRel)
		}
	}()

	if err := l.unpack(ctx, zr, tmpRel, progress); err != nil {
		return Path{}, err
	}

	if err := l.root.Rename(tmpRel, targetRel); err != nil {
		return Path{}, mapErr(err)
	}
	committed = true
	return target, nil
}

// preflightZip refuses an archive whose declared shape already exceeds the
// ceilings, before a single byte is written. The header can lie about sizes, so
// this is a cheap first gate, not the only one — the write path enforces the
// real totals as it goes.
func preflightZip(zr *zip.Reader) error {
	if len(zr.File) > MaxArchiveEntries {
		return fmt.Errorf("%w: %d entries exceeds the %d limit", ErrArchiveTooLarge, len(zr.File), MaxArchiveEntries)
	}
	var declared uint64
	for _, e := range zr.File {
		declared += e.UncompressedSize64
		if declared > MaxExtractedBytes {
			return fmt.Errorf("%w: declared size exceeds %d bytes", ErrArchiveTooLarge, uint64(MaxExtractedBytes))
		}
	}
	return nil
}

// unpack writes every entry of the archive under dstRoot, enforcing the byte
// and ratio ceilings against what is actually written.
func (l *Local) unpack(ctx context.Context, zr *zip.Reader, dstRoot string, progress func(fraction float64)) error {
	if err := l.root.Mkdir(dstRoot, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return mapErr(err)
	}

	var written int64
	total := zipTotalBytes(zr)
	for _, entry := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := safeJoin(dstRoot, entry.Name)
		if err != nil {
			return err
		}
		// A directory entry, or a name ending in a slash, is a directory.
		if entry.FileInfo().IsDir() {
			if err := l.mkdirAllUnder(rel); err != nil {
				return err
			}
			continue
		}
		// Symlinks, devices and the like are ignored: reproducing a symlink from
		// an untrusted archive is how one points at a file outside the tree.
		if !entry.Mode().IsRegular() {
			continue
		}
		n, err := l.extractFile(ctx, entry, rel, written)
		if err != nil {
			return err
		}
		written += n
		if written > MaxExtractedBytes {
			return fmt.Errorf("%w: extracted size exceeds %d bytes", ErrArchiveTooLarge, int64(MaxExtractedBytes))
		}
		if progress != nil && total > 0 {
			progress(float64(written) / float64(total))
		}
	}
	return nil
}

// extractFile writes one archive entry to rel and returns the bytes written. It
// caps the write at what remains of the global budget and refuses an entry that
// expands past [MaxCompressionRatio] relative to its compressed size — measured
// on bytes actually decompressed, so a header that understates the size cannot
// evade the check.
func (l *Local) extractFile(ctx context.Context, entry *zip.File, rel string, already int64) (int64, error) {
	parent := rel[:strings.LastIndex(rel, "/")+1]
	if parent != "" {
		if err := l.mkdirAllUnder(strings.TrimSuffix(parent, "/")); err != nil {
			return 0, err
		}
	}

	src, err := entry.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	defer src.Close()

	dst, err := l.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, mapErr(err)
	}
	defer dst.Close()

	// The ceiling for this entry: whichever is smaller of what remains of the
	// global budget and the ratio allowance over its compressed size. Reading
	// one byte past it means the entry lied about how far it expands.
	//
	// The ratio allowance is computed in the uint64 domain and only converted
	// once it is known to fit the global budget, so a header claiming an
	// enormous compressed size cannot overflow the multiplication and wrap into
	// a permissive ceiling.
	remaining := int64(MaxExtractedBytes) - already
	ceiling := remaining
	if entry.CompressedSize64 <= uint64(MaxExtractedBytes)/MaxCompressionRatio {
		// #nosec G115 -- guarded above: the product is at most MaxExtractedBytes, well within int64.
		ratio := int64(entry.CompressedSize64)*MaxCompressionRatio + ratioFloorBytes
		if ratio < ceiling {
			ceiling = ratio
		}
	}

	written, err := io.Copy(dst, io.LimitReader(ctxReader{ctx, src}, ceiling+1))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	if written > ceiling {
		return 0, fmt.Errorf("%w: an entry expands beyond the ratio allowed", ErrArchiveTooLarge)
	}
	if err := dst.Sync(); err != nil {
		return 0, mapErr(err)
	}
	return written, nil
}

// mkdirAllUnder creates rel and any missing parents, all already validated by
// safeJoin, treating an existing directory as success.
func (l *Local) mkdirAllUnder(rel string) error {
	if err := l.root.MkdirAll(rel, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return mapErr(err)
	}
	return nil
}

// safeJoin turns an archive entry name into a path under root, rejecting
// anything that would escape it. Each component is validated exactly as a path
// from a client is, so a traversal or an illegal byte is refused here rather
// than reaching the disk.
func safeJoin(root, name string) (string, error) {
	// Zip names are slash-separated by spec; a backslash is a literal character
	// in a name, not a separator, but Windows producers misuse it, so treat it
	// as one to avoid smuggling a component past validation.
	name = strings.ReplaceAll(name, "\\", "/")
	rel := root
	for _, comp := range strings.Split(name, "/") {
		if comp == "" || comp == "." {
			continue
		}
		if err := validateComponent(comp); err != nil {
			return "", fmt.Errorf("%w: %v", ErrBadArchive, err)
		}
		rel += "/" + comp
	}
	if rel == root {
		return "", fmt.Errorf("%w: entry has an empty name", ErrBadArchive)
	}
	return rel, nil
}

// zipTotalBytes sums the declared uncompressed sizes, for scaling progress. It
// is only a hint: the real total is enforced as bytes are written.
func zipTotalBytes(zr *zip.Reader) uint64 {
	var total uint64
	for _, e := range zr.File {
		total += e.UncompressedSize64
	}
	return total
}

// newTempDir reserves an empty directory inside the uploads area for an
// extraction in flight, so a half-written tree is never visible in a listing
// and sits on the same filesystem as its destination — keeping the final
// rename instant.
func (l *Local) newTempDir() (string, error) {
	base, err := l.newTempPath()
	if err != nil {
		return "", err
	}
	if err := l.root.Mkdir(base, 0o755); err != nil {
		return "", mapErr(err)
	}
	return base, nil
}

// ctxReader stops a decompression when the request is abandoned, checked
// between reads rather than on every byte.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
