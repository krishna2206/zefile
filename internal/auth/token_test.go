package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNewTokenIsUnpredictable(t *testing.T) {
	t.Parallel()

	const samples = 500
	seen := make(map[string]struct{}, samples)

	for range samples {
		token, hash, err := NewToken(SessionPrefix)
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if !strings.HasPrefix(token, SessionPrefix) {
			t.Fatalf("token %q lost its prefix", token)
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("NewToken repeated a value after %d samples", len(seen))
		}
		seen[token] = struct{}{}

		if !bytes.Equal(hash, HashToken(token)) {
			t.Fatal("the returned hash does not match HashToken of the token")
		}
		if bytes.Contains(hash, []byte(token)) {
			t.Fatal("the stored hash contains the token itself")
		}
	}
}

func TestHashTokenIsStable(t *testing.T) {
	t.Parallel()

	token, hash, err := NewToken(APIPrefix)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	// Lookup happens on every authenticated request, so the hash has to be
	// deterministic — unlike a password hash, which is salted per call.
	if !bytes.Equal(HashToken(token), hash) {
		t.Error("HashToken is not deterministic")
	}
	if bytes.Equal(HashToken(token), HashToken(token+"x")) {
		t.Error("two different tokens hashed alike")
	}
}

func TestTokenDisplayPrefix(t *testing.T) {
	t.Parallel()

	token, _, err := NewToken(APIPrefix)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	shown := TokenDisplayPrefix(token)
	if !strings.HasPrefix(shown, APIPrefix) {
		t.Errorf("display prefix %q lost the namespace", shown)
	}
	if len(shown) >= len(token) {
		t.Errorf("display prefix %q is not shorter than the token", shown)
	}
	// The point is recognising a token in a list without being able to use it.
	if strings.HasSuffix(token, shown) {
		t.Error("the display prefix reveals the end of the token")
	}
}

func TestPrefixesAreDistinct(t *testing.T) {
	t.Parallel()

	prefixes := []string{SessionPrefix, APIPrefix, InvitePrefix, SharePrefix}
	for i, a := range prefixes {
		for j, b := range prefixes {
			if i != j && strings.HasPrefix(a, b) {
				t.Errorf("prefix %q is a prefix of %q; a leaked token would be ambiguous", b, a)
			}
		}
	}
}

func TestLimiter(t *testing.T) {
	t.Parallel()

	now := time.Now()
	limiter := NewLimiter(3, time.Minute, func() time.Time { return now })

	if !limiter.Allow("key") {
		t.Fatal("a fresh key was blocked")
	}

	for i := range 3 {
		blocked := limiter.Fail("key")
		if i < 2 && blocked {
			t.Fatalf("blocked after %d failures, want 3", i+1)
		}
		if i == 2 && !blocked {
			t.Fatal("not blocked after reaching the limit")
		}
	}

	if limiter.Allow("key") {
		t.Error("Allow returned true past the limit")
	}
	if limiter.RetryAfter("key") <= 0 {
		t.Error("RetryAfter did not report a wait")
	}

	// Other keys are unaffected: one account being attacked must not lock out
	// everybody else.
	if !limiter.Allow("other") {
		t.Error("an unrelated key was blocked")
	}

	now = now.Add(time.Minute + time.Second)
	if !limiter.Allow("key") {
		t.Error("still blocked after the window elapsed")
	}
	if limiter.RetryAfter("key") != 0 {
		t.Error("RetryAfter still reports a wait after the window")
	}
}

func TestLimiterReset(t *testing.T) {
	t.Parallel()

	limiter := NewLimiter(3, time.Minute, nil)
	limiter.Fail("key")
	limiter.Fail("key")
	limiter.Reset("key")

	if !limiter.Allow("key") {
		t.Fatal("Reset did not clear the record")
	}
	if limiter.Fail("key"); !limiter.Allow("key") {
		t.Error("the counter did not restart from zero after Reset")
	}
}
