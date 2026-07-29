package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krishna2206/zefile/internal/db"
)

// Test password hashing runs at the cheapest settings Argon2 accepts. The
// defaults are calibrated to be slow on purpose, which would make this suite
// take minutes; the parameters under test are the ones in DefaultParams, and
// they are exercised by TestDefaultParamsAreDeliberate.
var testParams = Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16}

type fixture struct {
	svc   *Service
	clock *atomic.Int64
}

func (f *fixture) advance(d time.Duration) { f.clock.Add(int64(d / time.Second)) }

func newFixture(t *testing.T, opts ...Option) *fixture {
	t.Helper()

	database, err := db.Open(t.Context(), db.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC).Unix())

	base := []Option{
		WithParams(testParams),
		WithClock(func() time.Time { return time.Unix(clock.Load(), 0).UTC() }),
	}
	return &fixture{svc: New(database, append(base, opts...)...), clock: clock}
}

// setUp completes first-run setup and returns the administrator.
func (f *fixture) setUp(t *testing.T) User {
	t.Helper()
	token, err := f.svc.IssueSetupToken(t.Context())
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	user, err := f.svc.CompleteSetup(t.Context(), token, "krishna", "correct horse battery")
	if err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	return user
}

// TestLogoutIsImmediate is the completion criterion for this lot, and the
// defect that ended File Browser written as a test: a token must stop working
// on the very next request after signing out, with no grace period and no
// dependence on expiry.
func TestLogoutIsImmediate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()

	token, _, err := f.svc.CreateSession(ctx, user.ID, "curl", "10.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, _, err := f.svc.Lookup(ctx, token); err != nil {
		t.Fatalf("a fresh session did not resolve: %v", err)
	}

	if err := f.svc.RevokeToken(ctx, token); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	// No clock movement between revoking and checking: the token is dead now,
	// not merely destined to expire.
	if _, _, err := f.svc.Lookup(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("a revoked token still resolved: %v", err)
	}

	// Signing out twice is not an error; a client retrying after a dropped
	// connection must not see a failure.
	if err := f.svc.RevokeToken(ctx, token); err != nil {
		t.Errorf("second RevokeToken: %v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	t.Parallel()

	f := newFixture(t, WithSessionTTL(time.Hour))
	user := f.setUp(t)
	ctx := t.Context()

	token, session, err := f.svc.CreateSession(ctx, user.ID, "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	f.advance(59 * time.Minute)
	if _, _, err := f.svc.Lookup(ctx, token); err != nil {
		t.Fatalf("session died early: %v", err)
	}

	f.advance(2 * time.Minute)
	if _, _, err := f.svc.Lookup(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired session still resolved: %v", err)
	}

	// Purging is only housekeeping: the session already stopped working above.
	if err := f.svc.PurgeExpiredSessions(ctx); err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if _, _, err := f.svc.Lookup(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("after purge: %v", err)
	}
	_ = session
}

func TestRevokeAllSessions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()

	var tokens []string
	for _, device := range []string{"laptop", "phone", "tablet"} {
		token, _, err := f.svc.CreateSession(ctx, user.ID, device, "10.0.0.1")
		if err != nil {
			t.Fatalf("CreateSession(%s): %v", device, err)
		}
		tokens = append(tokens, token)
	}

	live, err := f.svc.ListSessions(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(live) != 3 {
		t.Fatalf("ListSessions = %d, want 3", len(live))
	}

	if err := f.svc.RevokeAllSessions(ctx, user.ID); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	for i, token := range tokens {
		if _, _, err := f.svc.Lookup(ctx, token); !errors.Is(err, ErrInvalidSession) {
			t.Errorf("token %d survived sign-out-everywhere: %v", i, err)
		}
	}
}

// TestAuthenticateDoesNotRevealWhichAccountsExist checks that a wrong password
// and an unknown account are indistinguishable to the caller.
func TestAuthenticateDoesNotRevealWhichAccountsExist(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.setUp(t)
	ctx := t.Context()

	_, wrongPassword := f.svc.Authenticate(ctx, "krishna", "not the password", "10.0.0.1")
	_, unknownAccount := f.svc.Authenticate(ctx, "nobody", "not the password", "10.0.0.2")

	if !errors.Is(wrongPassword, ErrInvalidCredentials) || !errors.Is(unknownAccount, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials for both, got %v and %v", wrongPassword, unknownAccount)
	}
	if wrongPassword.Error() != unknownAccount.Error() {
		t.Errorf("errors are distinguishable: %q vs %q", wrongPassword, unknownAccount)
	}
}

func TestAuthenticateSucceeds(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	created := f.setUp(t)

	user, err := f.svc.Authenticate(t.Context(), "krishna", "correct horse battery", "10.0.0.1")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user.ID != created.ID {
		t.Errorf("ID = %d, want %d", user.ID, created.ID)
	}
	if !user.IsAdmin {
		t.Error("the first account is not an administrator")
	}
}

func TestLoginThrottling(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.setUp(t)
	ctx := t.Context()

	for i := 0; i < defaultAccountLimit; i++ {
		if _, err := f.svc.Authenticate(ctx, "krishna", "wrong", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}

	// Blocked now — and the correct password must not get through either, or
	// throttling would be trivially bypassed by the attacker who finds it.
	if _, err := f.svc.Authenticate(ctx, "krishna", "correct horse battery", "10.0.0.1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after the limit: %v", err)
	}

	f.advance(defaultLoginWindow + time.Minute)
	if _, err := f.svc.Authenticate(ctx, "krishna", "correct horse battery", "10.0.0.1"); err != nil {
		t.Fatalf("after the window elapsed: %v", err)
	}
}

// TestSuccessClearsThrottle keeps a legitimate user from staying locked out by
// their own earlier typos.
func TestSuccessClearsThrottle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.setUp(t)
	ctx := t.Context()

	for i := 0; i < defaultAccountLimit-1; i++ {
		_, _ = f.svc.Authenticate(ctx, "krishna", "wrong", "10.0.0.1")
	}
	if _, err := f.svc.Authenticate(ctx, "krishna", "correct horse battery", "10.0.0.1"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	for i := 0; i < defaultAccountLimit-1; i++ {
		if _, err := f.svc.Authenticate(ctx, "krishna", "wrong", "10.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("the counter was not cleared by success: %v", err)
		}
	}
}

func TestSetup(t *testing.T) {
	t.Parallel()

	t.Run("token is single use", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()

		token, err := f.svc.IssueSetupToken(ctx)
		if err != nil {
			t.Fatalf("IssueSetupToken: %v", err)
		}
		if _, err := f.svc.CompleteSetup(ctx, token, "krishna", "correct horse battery"); err != nil {
			t.Fatalf("CompleteSetup: %v", err)
		}
		if _, err := f.svc.CompleteSetup(ctx, token, "intruder", "correct horse battery"); !errors.Is(err, ErrAlreadySetUp) {
			t.Fatalf("the setup token worked twice: %v", err)
		}
	})

	t.Run("issuing again invalidates the previous token", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()

		first, err := f.svc.IssueSetupToken(ctx)
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		second, err := f.svc.IssueSetupToken(ctx)
		if err != nil {
			t.Fatalf("second: %v", err)
		}

		// A token printed to a log the operator has since rotated away must
		// stop being useful.
		if _, err := f.svc.CompleteSetup(ctx, first, "krishna", "correct horse battery"); !errors.Is(err, ErrInvalidSetupToken) {
			t.Fatalf("the superseded token still worked: %v", err)
		}
		if _, err := f.svc.CompleteSetup(ctx, second, "krishna", "correct horse battery"); err != nil {
			t.Fatalf("the current token failed: %v", err)
		}
	})

	t.Run("token expires", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()

		token, err := f.svc.IssueSetupToken(ctx)
		if err != nil {
			t.Fatalf("IssueSetupToken: %v", err)
		}
		f.advance(DefaultSetupTTL + time.Hour)
		if _, err := f.svc.CompleteSetup(ctx, token, "krishna", "correct horse battery"); !errors.Is(err, ErrInvalidSetupToken) {
			t.Fatalf("an expired setup token worked: %v", err)
		}
	})

	t.Run("closed once an account exists", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.setUp(t)

		if _, err := f.svc.IssueSetupToken(t.Context()); !errors.Is(err, ErrAlreadySetUp) {
			t.Fatalf("a setup token was issued for a configured instance: %v", err)
		}
		needed, err := f.svc.NeedsSetup(t.Context())
		if err != nil {
			t.Fatalf("NeedsSetup: %v", err)
		}
		if needed {
			t.Error("NeedsSetup is still true after setup")
		}
	})

	t.Run("weak passwords are refused", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ctx := t.Context()

		token, err := f.svc.IssueSetupToken(ctx)
		if err != nil {
			t.Fatalf("IssueSetupToken: %v", err)
		}
		if _, err := f.svc.CompleteSetup(ctx, token, "krishna", "short"); err == nil {
			t.Fatal("a password below the minimum length was accepted")
		}
		// The failed attempt must not have consumed the token.
		if _, err := f.svc.CompleteSetup(ctx, token, "krishna", "correct horse battery"); err != nil {
			t.Fatalf("the token was consumed by a rejected attempt: %v", err)
		}
	})
}

// TestSetupIsAtomic checks the transaction: a token must never be left usable
// alongside an account that already exists, nor consumed without one.
func TestSetupIsAtomic(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	token, err := f.svc.IssueSetupToken(ctx)
	if err != nil {
		t.Fatalf("IssueSetupToken: %v", err)
	}
	if _, err := f.svc.CompleteSetup(ctx, token, "krishna", "correct horse battery"); err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}

	var unused int
	err = f.svc.database.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM invitations WHERE used_at IS NULL`).Scan(&unused)
	if err != nil {
		t.Fatalf("count invitations: %v", err)
	}
	if unused != 0 {
		t.Errorf("%d unused invitations remain after setup", unused)
	}
}

func TestTouchUpdatesLastSeen(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()

	token, session, err := f.svc.CreateSession(ctx, user.ID, "laptop", "10.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	f.advance(time.Hour)
	f.svc.Touch(ctx, session.ID)

	updated, _, err := f.svc.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !updated.LastSeenAt.After(session.LastSeenAt) {
		t.Errorf("LastSeenAt = %v, want later than %v", updated.LastSeenAt, session.LastSeenAt)
	}
}

func TestLookupRejectsGarbage(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.setUp(t)

	for _, token := range []string{"", "nonsense", SessionPrefix, SessionPrefix + "AAAA"} {
		if _, _, err := f.svc.Lookup(context.Background(), token); !errors.Is(err, ErrInvalidSession) {
			t.Errorf("Lookup(%q) = %v, want ErrInvalidSession", token, err)
		}
	}
}
