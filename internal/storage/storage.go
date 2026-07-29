package storage

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// Reserved directory names at the top of the storage tree. They hold Zefile's
// own working data and are hidden from listings and refused to callers.
//
// They live inside the storage root rather than beside it because deleting a
// large file must be a rename, not a copy — and a rename is only instantaneous
// within one filesystem. The same applies to in-flight uploads, which are moved
// into place on completion.
const (
	TrashDir   = ".zefile-trash"
	UploadsDir = ".zefile-uploads"
)

// DefaultReserve is the free space kept in hand by default.
//
// Its purpose is not to protect the files but to protect the database: if the
// disk fills completely, SQLite can no longer write, so sessions stop working
// and nobody can sign in to clean up. The service must degrade to read-only
// while it still has room to breathe.
const DefaultReserve = 256 << 20 // 256 MiB

// FileInfo describes one entry. Paths are always in key form.
type FileInfo struct {
	Path    Path
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool

	// Symlink reports whether the entry is a symbolic link. Links are listed
	// so they are visible, but they are never followed out of the root: os.Root
	// refuses any link resolving outside the tree.
	Symlink bool
}

// File is an open file for reading. Seek support is required rather than
// optional because HTTP range requests depend on it, and range requests are
// what make resumable and parallel downloads work.
type File interface {
	io.ReadSeekCloser
	Stat() (fs.FileInfo, error)
}

// WriteFile is an open file for writing.
//
// WriteAt is part of the contract because resumable uploads write chunks at
// arbitrary offsets; a plain io.Writer would force buffering a whole transfer.
type WriteFile interface {
	io.WriteCloser
	io.WriterAt
	Sync() error
}

// SpaceInfo reports the state of the underlying volume.
type SpaceInfo struct {
	// Available is the free space usable by an unprivileged process.
	Available uint64

	// Total is the size of the volume.
	Total uint64

	// Reserve is the threshold below which writes are refused.
	Reserve uint64

	// ReadOnly reports whether writes are currently refused, either by
	// configuration or because Available fell below Reserve.
	ReadOnly bool
}

// FS is the contract every storage backend satisfies. Only a local filesystem
// implementation exists today; the interface marks the seam where object
// storage or a remote backend would attach.
//
// Every method authorises before it acts. A caller holding an FS holds no
// privilege of its own.
type FS interface {
	// Stat returns information about a single entry.
	Stat(ctx context.Context, p Path) (FileInfo, error)

	// List returns the entries of a directory, excluding reserved ones.
	// The order is unspecified; sorting is a presentation concern.
	List(ctx context.Context, p Path) ([]FileInfo, error)

	// Open opens a file for reading.
	Open(ctx context.Context, p Path) (File, error)

	// Create creates or truncates a file and opens it for writing.
	Create(ctx context.Context, p Path) (WriteFile, error)

	// Mkdir creates a directory. Parents must already exist.
	Mkdir(ctx context.Context, p Path) error

	// MkdirAll creates a directory and any missing parents.
	MkdirAll(ctx context.Context, p Path) error

	// Move renames an entry. It never falls back to copying: a move that would
	// cross a filesystem boundary returns [ErrCrossDevice] rather than silently
	// turning an instant operation into a multi-gigabyte copy.
	Move(ctx context.Context, from, to Path) error

	// Remove deletes a file or an empty directory.
	Remove(ctx context.Context, p Path) error

	// RemoveAll deletes an entry and everything beneath it.
	RemoveAll(ctx context.Context, p Path) error

	// Space reports free space and whether writes are currently accepted.
	Space(ctx context.Context) (SpaceInfo, error)

	// Close releases the underlying handle.
	Close() error
}
