package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	t.Parallel()

	const password = "correct horse battery staple"

	hash, err := HashPassword(password, testParams)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("the hash contains the password")
	}

	ok, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("the correct password did not verify")
	}

	for _, wrong := range []string{
		"correct horse battery stapl",
		"Correct horse battery staple",
		"",
		password + " ",
	} {
		ok, err := VerifyPassword(wrong, hash)
		if err != nil {
			t.Fatalf("VerifyPassword(%q): %v", wrong, err)
		}
		if ok {
			t.Errorf("%q verified against a different password", wrong)
		}
	}
}

// TestHashesAreSalted checks that identical passwords produce different hashes.
// Without this, a database leak would reveal which accounts share a password.
func TestHashesAreSalted(t *testing.T) {
	t.Parallel()

	const password = "the same password twice"

	first, err := HashPassword(password, testParams)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := HashPassword(password, testParams)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of one password are identical; the salt is not random")
	}

	for _, hash := range []string{first, second} {
		ok, err := VerifyPassword(password, hash)
		if err != nil || !ok {
			t.Errorf("hash %q did not verify: ok=%v err=%v", hash, ok, err)
		}
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"plain text", "hunter2"},
		{"wrong algorithm", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA"},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"truncated", "$argon2id$v=19$m=8,t=1,p=1$c2FsdA"},
		{"bad base64", "$argon2id$v=19$m=8,t=1,p=1$!!!!$!!!!"},
		{"missing parameters", "$argon2id$v=19$$c2FsdA$aGFzaA"},
		{"empty salt", "$argon2id$v=19$m=8,t=1,p=1$$aGFzaA"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, err := VerifyPassword("anything", tc.hash)
			if ok {
				t.Fatal("a malformed hash reported a match")
			}
			if err == nil {
				t.Fatal("a malformed hash produced no error")
			}
		})
	}
}

func TestVerifyRejectsFutureVersion(t *testing.T) {
	t.Parallel()

	// A hash written by a newer Argon2 must fail loudly rather than be
	// misinterpreted under this build's rules.
	_, err := VerifyPassword("x", "$argon2id$v=99$m=8,t=1,p=1$c2FsdA$aGFzaA")
	if !errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("error = %v, want ErrIncompatibleVersion", err)
	}
}

func TestNeedsRehash(t *testing.T) {
	t.Parallel()

	weak := Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16}
	strong := Params{Memory: 64, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}

	weakHash, err := HashPassword("password under old settings", weak)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	strongHash, err := HashPassword("password under new settings", strong)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !NeedsRehash(weakHash, strong) {
		t.Error("a hash made under weaker settings was not flagged for upgrade")
	}
	if NeedsRehash(strongHash, strong) {
		t.Error("a current hash was flagged for upgrade")
	}
	// Raising the cost must not invalidate existing hashes; they keep verifying
	// under their own embedded parameters until the next sign-in replaces them.
	if ok, err := VerifyPassword("password under old settings", weakHash); err != nil || !ok {
		t.Errorf("an old hash stopped verifying: ok=%v err=%v", ok, err)
	}
	// An unreadable hash cannot be verified either, so it is worth replacing.
	if !NeedsRehash("garbage", strong) {
		t.Error("an unreadable hash was not flagged")
	}
}

// TestDefaultParamsAreDeliberate guards the settings actually shipped. Someone
// lowering them to speed up a test suite should have to change this too.
func TestDefaultParamsAreDeliberate(t *testing.T) {
	t.Parallel()

	// OWASP's low-memory recommendation, chosen because each concurrent sign-in
	// allocates Memory bytes and Zefile runs on small machines.
	if DefaultParams.Memory < 19*1024 {
		t.Errorf("Memory = %d KiB, below the 19 MiB floor", DefaultParams.Memory)
	}
	if DefaultParams.Iterations < 2 {
		t.Errorf("Iterations = %d, want at least 2", DefaultParams.Iterations)
	}
	if DefaultParams.SaltLength < 16 {
		t.Errorf("SaltLength = %d, want at least 16", DefaultParams.SaltLength)
	}
	if DefaultParams.KeyLength < 32 {
		t.Errorf("KeyLength = %d, want at least 32", DefaultParams.KeyLength)
	}

	hash, err := HashPassword("real settings", DefaultParams)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if ok, err := VerifyPassword("real settings", hash); err != nil || !ok {
		t.Fatalf("the shipped settings do not round-trip: ok=%v err=%v", ok, err)
	}
}
