package acl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/krishna2206/zefile/internal/content"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// SubjectLoader builds an authorisation context from an account identifier.
//
// It exists for the content origin, which learns who a signed link belongs to
// but has no session to resolve. The account is re-read on every use rather
// than trusted from the link, so a disabled account or a withdrawn
// administrator role takes effect immediately instead of lingering for as long
// as an outstanding link.
type SubjectLoader struct {
	engine *Engine
	reads  *sqlcgen.Queries
}

// NewSubjectLoader builds a loader over an open database.
func NewSubjectLoader(database *db.DB, engine *Engine) *SubjectLoader {
	return &SubjectLoader{engine: engine, reads: sqlcgen.New(database.Read)}
}

// ContextFor returns a context carrying the account's subject.
func (l *SubjectLoader) ContextFor(ctx context.Context, userID int64) (context.Context, error) {
	user, err := l.reads.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, content.ErrUnknownSubject
		}
		return nil, fmt.Errorf("acl: load account: %w", err)
	}
	if user.Disabled == 1 {
		return nil, content.ErrUnknownSubject
	}

	subject, err := l.engine.LoadSubject(ctx, user.ID, user.IsAdmin == 1)
	if err != nil {
		return nil, err
	}
	return NewContext(ctx, subject), nil
}
