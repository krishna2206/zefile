package auth

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Credential limits.
//
// The minimum password length is the only strength rule. Composition
// requirements — an uppercase, a digit, a symbol — push people towards
// predictable substitutions of short passwords, which is worse than a long one
// made of ordinary words.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 1024
	MinUsernameLength = 2
	MaxUsernameLength = 64
)

// reservedUsernames are names that would be confusing or misleading to hold.
//
// Not a security control — nothing grants privilege by name — but an account
// called "admin" on an instance where it is not the administrator, or one
// called "zefile" appearing to speak for the software, invites the wrong
// assumption.
var reservedUsernames = map[string]bool{
	"admin": true, "administrator": true, "root": true, "system": true,
	"zefile": true, "support": true, "security": true, "api": true,
	"anonymous": true, "guest": true, "nobody": true, "me": true,
}

// ValidationError names the field a caller has to fix.
//
// Carrying the field is what lets the interface put the message beside the
// input it concerns rather than in a banner at the top of a form, leaving the
// reader to work out which of three boxes is wrong.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// Is makes every validation failure match a single sentinel, so callers that
// only need to distinguish "the input was wrong" from "something broke" can.
func (e *ValidationError) Is(target error) bool { return target == ErrInvalidInput }

// ErrInvalidInput matches any [ValidationError].
var ErrInvalidInput = errors.New("auth: invalid input")

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// NormaliseUsername returns the form a username is stored and matched under.
//
// Case is folded so that one person cannot end up with two accounts, and a
// second person cannot register the visually identical "Krishna" beside an
// existing "krishna" — which is impersonation, not a naming preference.
func NormaliseUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// ValidateUsername checks a username and returns its normalised form.
func ValidateUsername(username string) (string, error) {
	normalised := NormaliseUsername(username)

	if normalised == "" {
		return "", invalid("username", "Choose a username.")
	}
	if !utf8.ValidString(normalised) {
		return "", invalid("username", "That username contains characters we cannot store.")
	}

	// Counted in runes, not bytes: an accented name should not be measured as
	// longer than it looks.
	length := utf8.RuneCountInString(normalised)
	if length < MinUsernameLength {
		return "", invalid("username", fmt.Sprintf("Use at least %d characters.", MinUsernameLength))
	}
	if length > MaxUsernameLength {
		return "", invalid("username", fmt.Sprintf("Use at most %d characters.", MaxUsernameLength))
	}

	if reservedUsernames[normalised] {
		return "", invalid("username", "That name is reserved. Pick another.")
	}

	const separators = "-_."
	runes := []rune(normalised)

	if !isUsernameStart(runes[0]) {
		return "", invalid("username", "Start with a letter or a digit.")
	}
	if strings.ContainsRune(separators, runes[len(runes)-1]) {
		return "", invalid("username", "End with a letter or a digit.")
	}

	for i, r := range runes {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			continue
		case strings.ContainsRune(separators, r):
			// Two separators in a row make near-identical names easy to
			// confuse, which is the same impersonation problem as case.
			if i > 0 && strings.ContainsRune(separators, runes[i-1]) {
				return "", invalid("username", "Do not repeat . - or _ next to each other.")
			}
		default:
			return "", invalid("username",
				"Use letters, digits, and . - or _ only.")
		}
	}

	return normalised, nil
}

// ValidatePassword checks a password against the username it will protect.
func ValidatePassword(password, username string) error {
	// Measured in runes so that a passphrase in a non-Latin script is not
	// rejected for being "short" when it is nothing of the sort.
	length := utf8.RuneCountInString(password)

	if length == 0 {
		return invalid("password", "Choose a password.")
	}
	if length < MinPasswordLength {
		return invalid("password",
			fmt.Sprintf("Use at least %d characters. A few ordinary words beat a short, clever one.", MinPasswordLength))
	}
	if length > MaxPasswordLength {
		// Argon2's cost grows with the input, so an unbounded password is a way
		// to make the server work hard on request.
		return invalid("password", fmt.Sprintf("Use at most %d characters.", MaxPasswordLength))
	}
	if strings.TrimSpace(password) == "" {
		return invalid("password", "A password of only spaces is not one.")
	}

	// Length alone is satisfied by twelve of the same character, which is not
	// what the rule is for.
	if distinctRunes(password) < 4 {
		return invalid("password", "Use more variety than a repeated character.")
	}

	if normalised := NormaliseUsername(username); normalised != "" {
		lower := strings.ToLower(password)
		if lower == normalised || strings.Contains(lower, normalised) && len(normalised) >= 4 {
			return invalid("password", "Do not build the password out of your username.")
		}
	}

	return nil
}

func isUsernameStart(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func distinctRunes(s string) int {
	seen := make(map[rune]struct{}, len(s))
	for _, r := range s {
		seen[r] = struct{}{}
	}
	return len(seen)
}
