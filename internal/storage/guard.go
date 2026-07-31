package storage

import "context"

// Op is the kind of access an operation needs. It is deliberately coarse: the
// storage layer asks "may this be read, written, or deleted", and the ACL
// engine decides. Storage has no opinion on users, groups or inheritance.
type Op uint8

const (
	// OpRead covers stat, list and open for reading.
	OpRead Op = iota + 1

	// OpWrite covers create, mkdir, and the destination of a move.
	OpWrite

	// OpDelete covers remove, and the source of a move — moving a file out of
	// a directory removes it from that directory, so read access alone must
	// never be enough.
	OpDelete
)

// String implements fmt.Stringer.
func (o Op) String() string {
	switch o {
	case OpRead:
		return "read"
	case OpWrite:
		return "write"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// Guard decides whether an operation is allowed. The identity performing it
// travels in the context, put there by the authentication middleware, so that
// no call site can pass a different subject than the authenticated one.
//
// Implementations return [ErrPermission] to refuse. Any other error is treated
// as a failure to decide and refuses the operation too: a Guard that cannot
// reach its data must never fall open.
type Guard interface {
	// Authorize reports whether one operation on one path may proceed.
	Authorize(ctx context.Context, op Op, p Path) error

	// Permitted answers the same question for many paths at once, returning one
	// verdict per input in the same order.
	//
	// It exists because listing a directory has to hide entries the caller may
	// not see — otherwise a listing leaks the names of files denied to them.
	// Asking Authorize per entry would mean one database round trip per file,
	// which a directory of ten thousand entries cannot afford.
	Permitted(ctx context.Context, op Op, paths []Path) ([]bool, error)
}

// ListGuard is an optional refinement of [Guard] for directory listings.
//
// A plain Guard answers read/write/delete per path, which is enough for opening
// a file but too blunt for listing: a user granted access to /projects/site
// cannot open the root to reach it, because they cannot read the root itself.
// A ListGuard lets such a user list the directories on the way to what they were
// granted, seeing only the branch that leads there. Storage uses it when the
// Guard implements it, and falls back to strict read otherwise.
type ListGuard interface {
	// CanList reports whether the caller may list a directory at all — because
	// they can read it, or because it leads to something they can read. The root
	// is always listable; its own contents are still filtered per entry.
	CanList(ctx context.Context, dir Path) (bool, error)

	// VisibleChildren returns, per path, whether it should appear in a listing:
	// true when the caller can read it, or when it is a directory leading to
	// something they can read.
	VisibleChildren(ctx context.Context, paths []Path) ([]bool, error)
}

// AllowAll permits everything. It is the Guard used by tests and by the
// single-user phase, before the ACL engine is wired in.
//
// It is a named type rather than a nil check so that "no authorisation" is
// something a caller writes down deliberately, and something that shows up in
// a grep.
type AllowAll struct{}

// Authorize implements [Guard].
func (AllowAll) Authorize(context.Context, Op, Path) error { return nil }

// Permitted implements [Guard].
func (AllowAll) Permitted(_ context.Context, _ Op, paths []Path) ([]bool, error) {
	verdicts := make([]bool, len(paths))
	for i := range verdicts {
		verdicts[i] = true
	}
	return verdicts, nil
}
