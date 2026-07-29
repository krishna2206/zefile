// Package auth owns accounts, passwords and sessions.
//
// # Sessions are rows, not claims
//
// A session token carries no information: it is a random string whose hash is
// looked up on every request. Ending a session means deleting or revoking that
// row, and the effect is immediate and total.
//
// This is the single most important departure from File Browser, whose
// self-contained JWTs could not be revoked — logging out left a stolen token
// working, and expired tokens kept being accepted in some paths. Expiry and
// revocation are filtered inside the SQL query here, so a caller that forgets
// to check the clock still cannot resurrect a dead session.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// Defaults for a new [Service].
const (
	// DefaultSessionTTL is long because this is a file server: being signed out
	// mid-upload is a real cost, and revocation is available at any moment.
	DefaultSessionTTL = 30 * 24 * time.Hour

	// DefaultSetupTTL bounds the first-run link. Long enough to read the logs
	// after a deployment, short enough that a forgotten instance does not sit
	// with an open door.
	DefaultSetupTTL = 24 * time.Hour

	// Login throttling. The per-address limit is looser than the per-account
	// one: several people can share an address, but nobody has a legitimate
	// reason to fail against one account ten times in a quarter of an hour.
	defaultIPLimit      = 20
	defaultAccountLimit = 10
	defaultLoginWindow  = 15 * time.Minute
)

// Errors returned by [Service].
var (
	// ErrInvalidCredentials covers an unknown account, a wrong password and a
	// disabled account alike. Distinguishing them would turn the sign-in form
	// into a way to enumerate accounts.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")

	// ErrRateLimited means too many recent failures for this account or address.
	ErrRateLimited = errors.New("auth: too many attempts")

	// ErrInvalidSession means the token is unknown, expired or revoked.
	ErrInvalidSession = errors.New("auth: invalid session")

	// ErrInvalidSetupToken means the first-run token is unknown, used or expired.
	ErrInvalidSetupToken = errors.New("auth: invalid setup token")

	// ErrAlreadySetUp means an account already exists, so first-run setup is
	// closed. It stays closed even if every account is later deleted.
	ErrAlreadySetUp = errors.New("auth: instance is already set up")
)

// User is an account, without its secrets.
type User struct {
	ID        int64
	Username  string
	Email     string
	IsAdmin   bool
	CreatedAt time.Time
}

// Session is an active sign-in.
type Session struct {
	ID         int64
	UserID     int64
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
	IP         string
}

// Service performs authentication against the database.
type Service struct {
	database *db.DB
	reads    *sqlcgen.Queries
	writes   *sqlcgen.Queries

	params     Params
	sessionTTL time.Duration
	setupTTL   time.Duration
	now        func() time.Time

	byAddress *Limiter
	byAccount *Limiter
}

// Option adjusts a [Service].
type Option func(*Service)

// WithClock replaces the time source, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithSessionTTL sets how long a new session stays valid.
func WithSessionTTL(d time.Duration) Option {
	return func(s *Service) { s.sessionTTL = d }
}

// WithParams overrides the password hashing cost.
func WithParams(p Params) Option {
	return func(s *Service) { s.params = p }
}

// New builds a Service over an open database.
func New(database *db.DB, opts ...Option) *Service {
	s := &Service{
		database:   database,
		reads:      sqlcgen.New(database.Read),
		writes:     sqlcgen.New(database.Write),
		params:     DefaultParams,
		sessionTTL: DefaultSessionTTL,
		setupTTL:   DefaultSetupTTL,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	// Built after the options so the limiters share whatever clock the service
	// ended up with.
	s.byAddress = NewLimiter(defaultIPLimit, defaultLoginWindow, s.now)
	s.byAccount = NewLimiter(defaultAccountLimit, defaultLoginWindow, s.now)
	return s
}

// ---------------------------------------------------------------- first run

// NeedsSetup reports whether the instance has no account yet.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	count, err := s.reads.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: count users: %w", err)
	}
	return count == 0, nil
}

// IssueSetupToken mints a fresh first-run token, invalidating any previous one.
//
// It is called at startup while no account exists, and the caller prints the
// resulting URL to the logs. Replacing the previous token on every start means
// the most recent log line is always the one that works — and that a token
// printed to a log an operator has since rotated away stops being useful.
func (s *Service) IssueSetupToken(ctx context.Context) (string, error) {
	needed, err := s.NeedsSetup(ctx)
	if err != nil {
		return "", err
	}
	if !needed {
		return "", ErrAlreadySetUp
	}

	if err := s.writes.DeleteUnusedInvitations(ctx); err != nil {
		return "", fmt.Errorf("auth: clear previous setup tokens: %w", err)
	}

	token, hash, err := NewToken(InvitePrefix)
	if err != nil {
		return "", err
	}
	now := s.now()
	if _, err := s.writes.CreateInvitation(ctx, sqlcgen.CreateInvitationParams{
		TokenHash: hash,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(s.setupTTL).Unix(),
	}); err != nil {
		return "", fmt.Errorf("auth: store setup token: %w", err)
	}
	return token, nil
}

// CompleteSetup consumes a first-run token and creates the administrator.
//
// The whole operation runs in one transaction: a crash between creating the
// account and consuming the token would otherwise leave a valid setup link on
// an instance that already has an administrator.
func (s *Service) CompleteSetup(ctx context.Context, token, username, password string) (User, error) {
	needed, err := s.NeedsSetup(ctx)
	if err != nil {
		return User{}, err
	}
	if !needed {
		return User{}, ErrAlreadySetUp
	}
	if err := validateCredentials(username, password); err != nil {
		return User{}, err
	}

	now := s.now()
	invitation, err := s.reads.GetInvitationByTokenHash(ctx, sqlcgen.GetInvitationByTokenHashParams{
		TokenHash: HashToken(token),
		ExpiresAt: now.Unix(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrInvalidSetupToken
		}
		return User{}, fmt.Errorf("auth: look up setup token: %w", err)
	}

	hash, err := HashPassword(password, s.params)
	if err != nil {
		return User{}, err
	}

	tx, err := s.database.Write.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("auth: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := s.writes.WithTx(tx)
	created, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		IsAdmin:      1,
		CreatedAt:    now.Unix(),
		UpdatedAt:    now.Unix(),
	})
	if err != nil {
		return User{}, fmt.Errorf("auth: create administrator: %w", err)
	}
	if err := q.MarkInvitationUsed(ctx, sqlcgen.MarkInvitationUsedParams{
		UsedAt: sql.NullInt64{Int64: now.Unix(), Valid: true},
		ID:     invitation.ID,
	}); err != nil {
		return User{}, fmt.Errorf("auth: consume setup token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("auth: commit: %w", err)
	}

	return toUser(created), nil
}

// -------------------------------------------------------------------- login

// Authenticate verifies a username and password.
//
// address is used for throttling and may be empty. The returned error is the
// same whatever went wrong, so nothing about which accounts exist leaks.
func (s *Service) Authenticate(ctx context.Context, username, password, address string) (User, error) {
	if address != "" && !s.byAddress.Allow(address) {
		return User{}, ErrRateLimited
	}
	if !s.byAccount.Allow(username) {
		return User{}, ErrRateLimited
	}

	row, err := s.reads.GetUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("auth: look up account: %w", err)
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Verify against a decoy hash anyway. Returning early here would make
		// an unknown account answer measurably faster than a wrong password,
		// which is enough to enumerate accounts over a few thousand requests.
		_, _ = VerifyPassword(password, decoyHash())
		s.recordFailure(username, address)
		return User{}, ErrInvalidCredentials
	}

	ok, err := VerifyPassword(password, row.PasswordHash)
	if err != nil || !ok {
		s.recordFailure(username, address)
		return User{}, ErrInvalidCredentials
	}

	s.byAccount.Reset(username)
	if address != "" {
		s.byAddress.Reset(address)
	}

	// A successful sign-in is the only moment the plaintext password is known,
	// so it is the only chance to upgrade a hash made under weaker settings.
	if NeedsRehash(row.PasswordHash, s.params) {
		if upgraded, hashErr := HashPassword(password, s.params); hashErr == nil {
			_ = s.writes.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
				PasswordHash: upgraded,
				UpdatedAt:    s.now().Unix(),
				ID:           row.ID,
			})
		}
	}

	return toUser(row), nil
}

func (s *Service) recordFailure(username, address string) {
	s.byAccount.Fail(username)
	if address != "" {
		s.byAddress.Fail(address)
	}
}

// ----------------------------------------------------------------- sessions

// CreateSession opens a session and returns the token to hand to the client.
// The plaintext is returned here and nowhere else; only its hash is stored.
func (s *Service) CreateSession(ctx context.Context, userID int64, userAgent, address string) (string, Session, error) {
	token, hash, err := NewToken(SessionPrefix)
	if err != nil {
		return "", Session{}, err
	}

	now := s.now()
	row, err := s.writes.CreateSession(ctx, sqlcgen.CreateSessionParams{
		UserID:     userID,
		TokenHash:  hash,
		CreatedAt:  now.Unix(),
		LastSeenAt: now.Unix(),
		ExpiresAt:  now.Add(s.sessionTTL).Unix(),
		UserAgent:  userAgent,
		Ip:         address,
	})
	if err != nil {
		return "", Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return token, toSession(row), nil
}

// Lookup resolves a session token to its session and account.
//
// Expiry, revocation and account status are all filtered by the query, so this
// cannot return something that merely looks valid.
func (s *Service) Lookup(ctx context.Context, token string) (Session, User, error) {
	row, err := s.reads.GetSessionByTokenHash(ctx, sqlcgen.GetSessionByTokenHashParams{
		TokenHash: HashToken(token),
		ExpiresAt: s.now().Unix(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, User{}, ErrInvalidSession
		}
		return Session{}, User{}, fmt.Errorf("auth: look up session: %w", err)
	}
	return toSession(row.Session), toUser(row.User), nil
}

// Touch records activity on a session, for the active-sessions screen.
//
// Failure is not fatal: losing a last-seen timestamp must never break a
// request that was otherwise perfectly authenticated.
func (s *Service) Touch(ctx context.Context, sessionID int64) {
	_ = s.writes.TouchSession(ctx, sqlcgen.TouchSessionParams{
		LastSeenAt: s.now().Unix(),
		ID:         sessionID,
	})
}

// RevokeToken ends the session identified by a token. It is the operation
// behind signing out, and takes effect on the very next request.
func (s *Service) RevokeToken(ctx context.Context, token string) error {
	session, _, err := s.Lookup(ctx, token)
	if err != nil {
		// Already invalid: signing out twice is not an error.
		if errors.Is(err, ErrInvalidSession) {
			return nil
		}
		return err
	}
	return s.RevokeSession(ctx, session.ID)
}

// RevokeSession ends one session by identifier, used by the active-sessions
// screen to sign out another device.
func (s *Service) RevokeSession(ctx context.Context, sessionID int64) error {
	if err := s.writes.RevokeSession(ctx, sqlcgen.RevokeSessionParams{
		RevokedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		ID:        sessionID,
	}); err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

// RevokeAllSessions signs an account out everywhere, the response to a stolen
// token or a changed password.
func (s *Service) RevokeAllSessions(ctx context.Context, userID int64) error {
	if err := s.writes.RevokeAllSessionsForUser(ctx, sqlcgen.RevokeAllSessionsForUserParams{
		RevokedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		UserID:    userID,
	}); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	return nil
}

// ListSessions returns the live sessions of an account, newest activity first.
func (s *Service) ListSessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.reads.ListSessionsForUser(ctx, sqlcgen.ListSessionsForUserParams{
		UserID:    userID,
		ExpiresAt: s.now().Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, toSession(row))
	}
	return sessions, nil
}

// PurgeExpiredSessions deletes rows past their expiry. Revoked and expired
// sessions already fail to resolve; this only keeps the table from growing.
func (s *Service) PurgeExpiredSessions(ctx context.Context) error {
	if err := s.writes.DeleteExpiredSessions(ctx, s.now().Unix()); err != nil {
		return fmt.Errorf("auth: purge sessions: %w", err)
	}
	return nil
}

// ------------------------------------------------------------------ helpers

// Credential limits. The minimum password length is the only strength rule:
// composition requirements push people towards predictable substitutions, and
// length is what actually costs an attacker.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 1024
	MaxUsernameLength = 64
)

func validateCredentials(username, password string) error {
	switch {
	case username == "":
		return errors.New("auth: username is required")
	case len(username) > MaxUsernameLength:
		return fmt.Errorf("auth: username is longer than %d characters", MaxUsernameLength)
	case len(password) < MinPasswordLength:
		return fmt.Errorf("auth: password must be at least %d characters", MinPasswordLength)
	case len(password) > MaxPasswordLength:
		// Argon2 cost grows with input, so an unbounded password is a way to
		// make the server work hard on request.
		return fmt.Errorf("auth: password is longer than %d characters", MaxPasswordLength)
	}
	return nil
}

// decoyHash is a real hash of a value nobody uses, verified against when an
// account does not exist so that both paths cost the same.
var decoyHash = sync.OnceValue(func() string {
	hash, err := HashPassword("decoy-for-constant-time-comparison", DefaultParams)
	if err != nil {
		// Only reachable if the system entropy source fails, in which case
		// nothing else in this package works either.
		panic("auth: cannot build decoy hash: " + err.Error())
	}
	return hash
})

func toUser(row sqlcgen.User) User {
	return User{
		ID:        row.ID,
		Username:  row.Username,
		Email:     row.Email.String,
		IsAdmin:   row.IsAdmin == 1,
		CreatedAt: time.Unix(row.CreatedAt, 0).UTC(),
	}
}

func toSession(row sqlcgen.Session) Session {
	return Session{
		ID:         row.ID,
		UserID:     row.UserID,
		CreatedAt:  time.Unix(row.CreatedAt, 0).UTC(),
		LastSeenAt: time.Unix(row.LastSeenAt, 0).UTC(),
		ExpiresAt:  time.Unix(row.ExpiresAt, 0).UTC(),
		UserAgent:  row.UserAgent,
		IP:         row.Ip,
	}
}
