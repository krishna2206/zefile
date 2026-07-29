package acl

import "context"

// Subject is who is acting. It is assembled once, when a request is
// authenticated, and travels in the context from there.
//
// Group membership is carried rather than looked up on demand: resolution runs
// on every storage operation, and re-reading membership each time would add a
// query to every request for data that cannot change mid-request.
type Subject struct {
	UserID  int64
	IsAdmin bool
	Groups  []int64
}

type contextKey struct{}

// NewContext returns a context carrying the subject.
//
// Only the authentication middleware should call this. Because the storage
// layer reads the subject from the context rather than taking it as an
// argument, no call site can act on behalf of someone other than the
// authenticated caller — there is no parameter to pass the wrong value to.
func NewContext(ctx context.Context, s Subject) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}

// FromContext returns the subject, if the context carries one.
//
// A context without a subject is anonymous, and anonymous is refused
// everywhere. Public share links do not go through this path: they are
// capabilities checked by the share subsystem, not identities.
func FromContext(ctx context.Context) (Subject, bool) {
	s, ok := ctx.Value(contextKey{}).(Subject)
	return s, ok
}
