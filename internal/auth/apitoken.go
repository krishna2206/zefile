package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// ScopeFull is the only scope for now: a token acts with the full authority of
// the account that created it, exactly as that account's own session would. The
// scopes column exists so finer grants can be added later without a migration.
const ScopeFull = "full"

// ErrInvalidToken means an API token is unknown, expired, revoked, or belongs
// to a disabled account. It mirrors ErrInvalidSession so the middleware can
// treat either credential the same way.
var ErrInvalidToken = errors.New("auth: invalid api token")

// ErrTokenNotFound means no live token with that id belongs to the caller.
var ErrTokenNotFound = errors.New("auth: no such api token")

// APIToken is a long-lived credential a user creates for programmatic access.
// The plaintext is shown once at creation and never stored; afterwards only the
// prefix identifies it in the interface.
type APIToken struct {
	ID         int64
	UserID     int64
	Name       string
	Prefix     string
	Scopes     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// CreateAPIToken mints a token for an account and returns the plaintext once.
//
// A nil expiresAt means the token never expires, which is the common case for a
// backup script or a CI job: revocation, not expiry, is the intended off switch.
func (s *Service) CreateAPIToken(ctx context.Context, userID int64, name string, expiresAt *time.Time) (string, APIToken, error) {
	token, hash, err := NewToken(APIPrefix)
	if err != nil {
		return "", APIToken{}, err
	}

	var expires sql.NullInt64
	if expiresAt != nil {
		expires = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}

	row, err := s.writes.CreateAPIToken(ctx, sqlcgen.CreateAPITokenParams{
		UserID:    userID,
		Name:      name,
		TokenHash: hash,
		Prefix:    TokenDisplayPrefix(token),
		Scopes:    ScopeFull,
		CreatedAt: s.now().Unix(),
		ExpiresAt: expires,
	})
	if err != nil {
		return "", APIToken{}, fmt.Errorf("auth: create api token: %w", err)
	}
	return token, toAPIToken(row), nil
}

// LookupAPIToken resolves a zefile_live_ token to its owner, applying the same
// expiry, revocation and account-enabled filters as a session lookup.
func (s *Service) LookupAPIToken(ctx context.Context, token string) (APIToken, User, error) {
	row, err := s.reads.GetAPITokenByHash(ctx, sqlcgen.GetAPITokenByHashParams{
		TokenHash: HashToken(token),
		ExpiresAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIToken{}, User{}, ErrInvalidToken
		}
		return APIToken{}, User{}, fmt.Errorf("auth: look up api token: %w", err)
	}
	return toAPIToken(row.ApiToken), toUser(row.User), nil
}

// TouchAPIToken records that a token was used. Best-effort, like session touch:
// losing a last-used timestamp must never fail an authenticated request.
func (s *Service) TouchAPIToken(ctx context.Context, id int64) {
	_ = s.writes.TouchAPIToken(ctx, sqlcgen.TouchAPITokenParams{
		LastUsedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		ID:         id,
	})
}

// ListAPITokens returns an account's live tokens, newest first.
func (s *Service) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.reads.ListAPITokensForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list api tokens: %w", err)
	}
	tokens := make([]APIToken, 0, len(rows))
	for _, row := range rows {
		tokens = append(tokens, toAPIToken(row))
	}
	return tokens, nil
}

// RevokeAPIToken ends one token, scoped to its owner so an id cannot be guessed
// to revoke someone else's. It returns ErrTokenNotFound when nothing matched.
func (s *Service) RevokeAPIToken(ctx context.Context, userID, id int64) error {
	rows, err := s.writes.RevokeAPITokenForUser(ctx, sqlcgen.RevokeAPITokenForUserParams{
		RevokedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		ID:        id,
		UserID:    userID,
	})
	if err != nil {
		return fmt.Errorf("auth: revoke api token: %w", err)
	}
	if rows == 0 {
		return ErrTokenNotFound
	}
	return nil
}

func toAPIToken(row sqlcgen.ApiToken) APIToken {
	t := APIToken{
		ID:        row.ID,
		UserID:    row.UserID,
		Name:      row.Name,
		Prefix:    row.Prefix,
		Scopes:    row.Scopes,
		CreatedAt: time.Unix(row.CreatedAt, 0).UTC(),
	}
	if row.LastUsedAt.Valid {
		used := time.Unix(row.LastUsedAt.Int64, 0).UTC()
		t.LastUsedAt = &used
	}
	if row.ExpiresAt.Valid {
		exp := time.Unix(row.ExpiresAt.Int64, 0).UTC()
		t.ExpiresAt = &exp
	}
	return t
}
