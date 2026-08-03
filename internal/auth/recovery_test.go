package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestRecoveryCodeResetsPassword(t *testing.T) {
	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()

	codes, err := f.svc.GenerateRecoveryCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), RecoveryCodeCount)
	}
	if !strings.Contains(codes[0], "-") {
		t.Errorf("code %q is not grouped for readability", codes[0])
	}

	if err := f.svc.ResetPasswordWithCode(ctx, "krishna", codes[0], "a whole new passphrase", "1.2.3.4"); err != nil {
		t.Fatalf("ResetPasswordWithCode: %v", err)
	}

	// The new password works and the old one does not.
	if _, err := f.svc.Authenticate(ctx, "krishna", "a whole new passphrase", "1.2.3.4"); err != nil {
		t.Fatalf("authenticate with new password: %v", err)
	}
	if _, err := f.svc.Authenticate(ctx, "krishna", "correct horse battery", "1.2.3.4"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password should be rejected, got %v", err)
	}

	// One code was spent.
	remaining, err := f.svc.RecoveryCodesRemaining(ctx, user.ID)
	if err != nil {
		t.Fatalf("RecoveryCodesRemaining: %v", err)
	}
	if remaining != RecoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d", remaining, RecoveryCodeCount-1)
	}
}

func TestRecoveryCodeIsSingleUse(t *testing.T) {
	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()

	codes, err := f.svc.GenerateRecoveryCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if err := f.svc.ResetPasswordWithCode(ctx, "krishna", codes[0], "first new passphrase", "1.2.3.4"); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	if err := f.svc.ResetPasswordWithCode(ctx, "krishna", codes[0], "second new passphrase", "1.2.3.4"); !errors.Is(err, ErrInvalidRecovery) {
		t.Fatalf("a reused code should be rejected, got %v", err)
	}
}

func TestRecoveryRejectsWrongCodeAndUnknownUser(t *testing.T) {
	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()
	if _, err := f.svc.GenerateRecoveryCodes(ctx, user.ID); err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	if err := f.svc.ResetPasswordWithCode(ctx, "krishna", "wrong-wrong", "a brand new passphrase", "1.2.3.4"); !errors.Is(err, ErrInvalidRecovery) {
		t.Errorf("wrong code should be rejected, got %v", err)
	}
	if err := f.svc.ResetPasswordWithCode(ctx, "nobody", "wrong-wrong", "a brand new passphrase", "5.6.7.8"); !errors.Is(err, ErrInvalidRecovery) {
		t.Errorf("unknown account should be rejected, got %v", err)
	}
}

func TestRegenerateInvalidatesOldCodes(t *testing.T) {
	f := newFixture(t)
	user := f.setUp(t)
	ctx := t.Context()

	first, err := f.svc.GenerateRecoveryCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := f.svc.GenerateRecoveryCodes(ctx, user.ID); err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	// A code from the first set no longer works after regeneration.
	if err := f.svc.ResetPasswordWithCode(ctx, "krishna", first[0], "a fresh new passphrase", "1.2.3.4"); !errors.Is(err, ErrInvalidRecovery) {
		t.Fatalf("old code should be invalid after regeneration, got %v", err)
	}
}
