package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	t.Parallel()

	accepted := []struct{ in, want string }{
		{"krishna", "krishna"},
		{"Krishna", "krishna"},     // folded
		{"  krishna  ", "krishna"}, // trimmed
		{"kr", "kr"},
		{"user.name", "user.name"},
		{"user-name", "user-name"},
		{"user_name", "user_name"},
		{"a1", "a1"},
		{"9lives", "9lives"},
		{"fitiavana", "fitiavana"},
		{"józef", "józef"}, // letters are letters, not just ASCII ones
	}
	for _, tc := range accepted {
		t.Run("ok/"+tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateUsername(tc.in)
			if err != nil {
				t.Fatalf("ValidateUsername(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateUsername(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	rejected := []struct{ name, in string }{
		{"empty", ""},
		{"only spaces", "   "},
		{"too short", "k"},
		{"too long", strings.Repeat("a", MaxUsernameLength+1)},
		{"leading separator", "-krishna"},
		{"leading dot", ".krishna"},
		{"trailing separator", "krishna-"},
		{"doubled separator", "kri..shna"},
		{"mixed doubled separator", "kri-_shna"},
		{"space inside", "kri shna"},
		{"slash", "kri/shna"},
		{"at sign", "kri@shna"},
		{"control character", "kri\x00shna"},
		{"newline", "kri\nshna"},
		{"reserved", "admin"},
		{"reserved in another case", "ADMIN"},
		{"reserved product name", "zefile"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateUsername(tc.in)
			if err == nil {
				t.Fatalf("ValidateUsername(%q) = %q, want an error", tc.in, got)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want it to match ErrInvalidInput", err)
			}

			// The field has to be named, or an interface cannot put the message
			// beside the input it concerns.
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("error %v is not a *ValidationError", err)
			}
			if invalid.Field != "username" {
				t.Errorf("Field = %q, want %q", invalid.Field, "username")
			}
			if invalid.Message == "" {
				t.Error("Message is empty")
			}
		})
	}
}

// TestUsernameFoldingPreventsImpersonation is the reason case is folded rather
// than preserved: "Krishna" registering beside an existing "krishna" is
// impersonation, not a naming preference.
func TestUsernameFoldingPreventsImpersonation(t *testing.T) {
	t.Parallel()

	for _, variant := range []string{"krishna", "Krishna", "KRISHNA", "KrIsHnA", " krishna "} {
		got, err := ValidateUsername(variant)
		if err != nil {
			t.Fatalf("ValidateUsername(%q): %v", variant, err)
		}
		if got != "krishna" {
			t.Errorf("ValidateUsername(%q) = %q, want them all to collapse to one name", variant, got)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"correct horse battery",
		"un mot de passe assez long",
		"Tr0ub4dor&3xxxxxxxx",
		"这是一个很长的密码短语句子", // 13 runes: measured in runes, not bytes
	}
	for _, password := range accepted {
		t.Run("ok/"+password, func(t *testing.T) {
			t.Parallel()
			if err := ValidatePassword(password, "krishna"); err != nil {
				t.Fatalf("ValidatePassword(%q): %v", password, err)
			}
		})
	}

	rejected := []struct{ name, password, username string }{
		{"empty", "", "krishna"},
		{"too short", "short", "krishna"},
		{"one below the floor", "abcdefghijk", "krishna"}, // 11 runes
		{"too long", strings.Repeat("a", MaxPasswordLength+1), "krishna"},
		{"only spaces", strings.Repeat(" ", MinPasswordLength+4), "krishna"},
		// Length alone is satisfied by twelve of the same character, which is
		// not what the rule exists for.
		{"one repeated character", strings.Repeat("a", MinPasswordLength+2), "krishna"},
		{"two characters alternating", strings.Repeat("ab", MinPasswordLength), "krishna"},
		{"the username itself", "krishnakrishna", "krishna"},
		{"the username padded", "xxkrishnaxxxxx", "krishna"},
		{"the username in another case", "KRISHNAkrishna", "krishna"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePassword(tc.password, tc.username)
			if err == nil {
				t.Fatal("want an error")
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) || invalid.Field != "password" {
				t.Fatalf("error = %v, want a *ValidationError naming the password field", err)
			}
		})
	}
}

// TestMinimumLengthIsDeliberate guards the shipped floor. Lowering it to make a
// test convenient should have to change this too.
func TestMinimumLengthIsDeliberate(t *testing.T) {
	t.Parallel()

	if MinPasswordLength < 12 {
		t.Errorf("MinPasswordLength = %d, below the intended floor of 12", MinPasswordLength)
	}
}
