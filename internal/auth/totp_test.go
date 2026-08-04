package auth

import (
	"errors"
	"testing"
)

// TestTOTPMatchesRFC6238 checks the derivation against the published RFC 6238
// test vectors (SHA-1, secret "12345678901234567890"), so a subtle mistake in
// the HMAC or truncation is caught rather than shipped.
func TestTOTPMatchesRFC6238(t *testing.T) {
	key := []byte("12345678901234567890")
	cases := map[int64]string{
		1:        "287082", // T = 59s
		37037036: "081804", // T = 1111111109s
	}
	for counter, want := range cases {
		if got := totpCode(key, counter); got != want {
			t.Errorf("totpCode(counter=%d) = %s, want %s", counter, got, want)
		}
	}
}

func currentCode(t *testing.T, f *fixture, secret string) string {
	t.Helper()
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return totpCode(key, f.clock.Load()/totpPeriod)
}

func TestTOTPEnableAndVerify(t *testing.T) {
	f := newFixture(t)
	admin := f.setUp(t)
	ctx := t.Context()

	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}

	// A wrong code must not enable it.
	if err := f.svc.EnableTOTP(ctx, admin.ID, secret, "000000"); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("EnableTOTP with wrong code = %v, want ErrInvalidTOTP", err)
	}

	if err := f.svc.EnableTOTP(ctx, admin.ID, secret, currentCode(t, f, secret)); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}

	// The account now reports two-factor as enabled.
	user, err := f.svc.GetUser(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !user.TOTPEnabled {
		t.Error("expected TOTPEnabled to be true after enabling")
	}

	// A current code verifies at login; a wrong one does not.
	if err := f.svc.VerifyLoginTOTP(ctx, admin.ID, currentCode(t, f, secret)); err != nil {
		t.Errorf("VerifyLoginTOTP with valid code: %v", err)
	}
	if err := f.svc.VerifyLoginTOTP(ctx, admin.ID, "000000"); !errors.Is(err, ErrInvalidTOTP) {
		t.Errorf("VerifyLoginTOTP with wrong code = %v, want ErrInvalidTOTP", err)
	}
}

func TestTOTPRecoveryCodeBypassAtLogin(t *testing.T) {
	f := newFixture(t)
	admin := f.setUp(t)
	ctx := t.Context()

	secret, _ := GenerateTOTPSecret()
	if err := f.svc.EnableTOTP(ctx, admin.ID, secret, currentCode(t, f, secret)); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}

	codes, err := f.svc.GenerateRecoveryCodes(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	// A recovery code stands in for the authenticator, and is spent once.
	if err := f.svc.VerifyLoginTOTP(ctx, admin.ID, codes[0]); err != nil {
		t.Fatalf("VerifyLoginTOTP with recovery code: %v", err)
	}
	if err := f.svc.VerifyLoginTOTP(ctx, admin.ID, codes[0]); !errors.Is(err, ErrInvalidTOTP) {
		t.Errorf("reusing a recovery code = %v, want ErrInvalidTOTP", err)
	}
}

func TestDisableTOTP(t *testing.T) {
	f := newFixture(t)
	admin := f.setUp(t)
	ctx := t.Context()

	secret, _ := GenerateTOTPSecret()
	if err := f.svc.EnableTOTP(ctx, admin.ID, secret, currentCode(t, f, secret)); err != nil {
		t.Fatalf("EnableTOTP: %v", err)
	}

	if err := f.svc.DisableTOTP(ctx, admin.ID, "000000"); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("DisableTOTP with wrong code = %v, want ErrInvalidTOTP", err)
	}
	if err := f.svc.DisableTOTP(ctx, admin.ID, currentCode(t, f, secret)); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}

	user, _ := f.svc.GetUser(ctx, admin.ID)
	if user.TOTPEnabled {
		t.Error("expected TOTPEnabled to be false after disabling")
	}
}
