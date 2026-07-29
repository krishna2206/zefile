package storage

import "errors"

// Sentinel errors returned by this package. Callers should compare with
// errors.Is rather than matching on message text, and the HTTP layer maps each
// one to a stable machine-readable code.
var (
	// ErrInvalidPath means the path could not be parsed: it was malformed,
	// contained a traversal component, or used bytes a name may not contain.
	ErrInvalidPath = errors.New("storage: invalid path")

	// ErrReserved means the path targets a directory Zefile keeps for itself,
	// such as the trash or in-flight uploads.
	ErrReserved = errors.New("storage: reserved path")

	// ErrNotExist means no file or directory exists at the path.
	ErrNotExist = errors.New("storage: no such file or directory")

	// ErrExist means something already exists where one was to be created.
	ErrExist = errors.New("storage: file already exists")

	// ErrNotDir means a directory was required but the path is a file.
	ErrNotDir = errors.New("storage: not a directory")

	// ErrIsDir means a file was required but the path is a directory.
	ErrIsDir = errors.New("storage: is a directory")

	// ErrNotEmpty means a non-recursive remove was attempted on a directory
	// that still has entries.
	ErrNotEmpty = errors.New("storage: directory not empty")

	// ErrPermission means the Guard refused the operation. It carries no
	// detail on purpose: telling a caller why access was denied leaks the
	// existence and shape of paths they may not see.
	ErrPermission = errors.New("storage: permission denied")

	// ErrReadOnly means the instance is serving in read-only mode, either by
	// configuration or because free space fell below the reserve.
	ErrReadOnly = errors.New("storage: read-only mode")

	// ErrNoSpace means free space is below the configured reserve. Writes are
	// refused while reads, listings and deletions continue to work, so an
	// administrator can still sign in and reclaim space.
	ErrNoSpace = errors.New("storage: insufficient free space")

	// ErrCrossDevice means an operation would have to copy across filesystems
	// rather than rename within one. Relevant for trash and uploads, which must
	// live on the same filesystem as the data to stay instantaneous.
	ErrCrossDevice = errors.New("storage: cross-device operation")
)

// ErrAmbiguous means two entries in one directory normalise to the same name —
// the composed and decomposed spellings of the same word, for instance. Acting
// on either would mean acting on a file the caller did not ask for.
var ErrAmbiguous = errors.New("storage: ambiguous name")
