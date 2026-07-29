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
	Authorize(ctx context.Context, op Op, p Path) error
}

// AllowAll permits everything. It is the Guard used by tests and by the
// single-user phase, before the ACL engine exists.
//
// It is a named type rather than a nil check so that "no authorisation" is
// something a caller writes down deliberately, and something that shows up in
// a grep when the ACL engine lands.
type AllowAll struct{}

// Authorize implements [Guard].
func (AllowAll) Authorize(context.Context, Op, Path) error { return nil }
