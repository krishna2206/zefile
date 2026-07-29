package db

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// TestGeneratedQueriesRunAgainstTheSchema is a smoke test for the whole
// generation pipeline: schema, queries, generated code and driver together.
// A schema change that silently breaks a query fails here rather than at the
// call site months later.
func TestGeneratedQueriesRunAgainstTheSchema(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})
	ctx := t.Context()
	writes := sqlcgen.New(d.Write)
	reads := sqlcgen.New(d.Read)

	count, err := reads.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountUsers on a fresh database = %d, want 0", count)
	}

	user, err := writes.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     "krishna",
		Email:        sql.NullString{String: "k@example.test", Valid: true},
		PasswordHash: "argon2id$placeholder",
		IsAdmin:      1,
		CreatedAt:    1000,
		UpdatedAt:    1000,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID == 0 {
		t.Error("CreateUser returned no id; RETURNING is not wired up")
	}

	session, err := writes.CreateSession(ctx, sqlcgen.CreateSessionParams{
		UserID:     user.ID,
		TokenHash:  []byte("hashed-token"),
		CreatedAt:  1000,
		LastSeenAt: 1000,
		ExpiresAt:  2000,
		UserAgent:  "test",
		Ip:         "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// The join returns both rows, which is what an authenticated request needs
	// in a single round trip.
	found, err := reads.GetSessionByTokenHash(ctx, sqlcgen.GetSessionByTokenHashParams{
		TokenHash: []byte("hashed-token"),
		ExpiresAt: 1500,
	})
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if found.User.Username != "krishna" {
		t.Errorf("joined user = %q, want %q", found.User.Username, "krishna")
	}

	// Expiry is enforced by the query itself, so a caller that forgets to check
	// the clock still cannot use a dead session.
	if _, err := reads.GetSessionByTokenHash(ctx, sqlcgen.GetSessionByTokenHashParams{
		TokenHash: []byte("hashed-token"),
		ExpiresAt: 9999,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("an expired session was returned: %v", err)
	}

	// The defect that ended File Browser, encoded as a test: revoking must take
	// effect on the very next lookup, with no grace period.
	if err := writes.RevokeSession(ctx, sqlcgen.RevokeSessionParams{
		RevokedAt: sql.NullInt64{Int64: 1200, Valid: true},
		ID:        session.ID,
	}); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := reads.GetSessionByTokenHash(ctx, sqlcgen.GetSessionByTokenHashParams{
		TokenHash: []byte("hashed-token"),
		ExpiresAt: 1500,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a revoked session still resolved: %v", err)
	}
}
