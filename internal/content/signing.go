// Package content serves user files from the content origin.
//
// Nothing here reads a cookie. That is the point of the separate origin: a file
// uploaded by one user cannot reach another user's session, because on this
// host there is no session to reach. Access is proven by the URL itself.
package content

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/krishna2206/zefile/internal/storage"
)

// DefaultTTL is how long a signed link stays valid.
//
// Short on purpose. The link is a capability: whoever holds it can read the
// file, with no further check. Minutes are enough to start a download — the
// transfer itself is not interrupted when the link expires — while keeping a
// URL that leaks into a log or a chat window from being useful for long.
const DefaultTTL = 5 * time.Minute

// Errors returned when verifying a link.
var (
	// ErrInvalidSignature means the link is malformed or was not signed by this
	// instance.
	ErrInvalidSignature = errors.New("content: invalid signature")

	// ErrExpired means the link was valid but its window has passed.
	ErrExpired = errors.New("content: link has expired")

	// ErrUnknownSubject means the account a link was minted for no longer
	// exists or has been disabled. A [SubjectLoader] returns it, and the server
	// answers it like any other refusal.
	ErrUnknownSubject = errors.New("content: unknown or disabled account")
)

// Signer mints and verifies short-lived download links.
//
// The key is generated at startup and never persisted, so a restart invalidates
// outstanding links. With a five-minute window that costs a user at most one
// re-click, and it removes a long-lived secret from the disk entirely.
type Signer struct {
	key []byte
	now func() time.Time
	ttl time.Duration
}

// NewSigner builds a Signer with a fresh random key.
func NewSigner() (*Signer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("content: generate signing key: %w", err)
	}
	return &Signer{key: key, now: time.Now, ttl: DefaultTTL}, nil
}

// Sign returns the opaque token identifying a file, for one account, for a
// limited time.
//
// The account is part of what is signed rather than being dropped once the link
// exists. A link is then not a bearer capability: it still has to survive the
// permission check at download time, so a right revoked in between takes effect
// and the storage layer remains the only place access is decided.
func (s *Signer) Sign(p storage.Path, userID int64) string {
	expires := s.now().Add(s.ttl).Unix()
	payload := strconv.FormatInt(expires, 10) + ":" +
		strconv.FormatInt(userID, 10) + ":" + p.String()

	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.mac(encoded))
}

// Verify checks a token and returns the path and the account it was minted for.
//
// The signature is checked before the expiry is even parsed. Reading anything
// out of an unauthenticated payload means acting on attacker-controlled data,
// however harmless the field looks.
func (s *Signer) Verify(token string) (storage.Path, int64, error) {
	encoded, signature, found := strings.Cut(token, ".")
	if !found {
		return storage.Path{}, 0, ErrInvalidSignature
	}

	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	// Constant time: a byte-by-byte comparison that returns early leaks how
	// much of a guess was right, which is enough to forge a signature one byte
	// at a time.
	if subtle.ConstantTimeCompare(provided, s.mac(encoded)) != 1 {
		return storage.Path{}, 0, ErrInvalidSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	rawExpiry, rest, found := strings.Cut(string(payload), ":")
	if !found {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	rawUser, rawPath, found := strings.Cut(rest, ":")
	if !found {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	expires, err := strconv.ParseInt(rawExpiry, 10, 64)
	if err != nil {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	userID, err := strconv.ParseInt(rawUser, 10, 64)
	if err != nil {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	if s.now().Unix() > expires {
		return storage.Path{}, 0, ErrExpired
	}

	// Parsed rather than trusted. The path was validated when the link was
	// minted, but re-parsing keeps this package from being the one place where
	// a path reaches storage without going through the usual door.
	p, err := storage.ParsePath(rawPath)
	if err != nil {
		return storage.Path{}, 0, ErrInvalidSignature
	}
	return p, userID, nil
}

// SignBundle signs a set of paths for one account, for a limited time. It is the
// token behind a multi-item zip download; each path is authorised again per
// entry when the archive is streamed.
func (s *Signer) SignBundle(paths []storage.Path, userID int64) string {
	expires := s.now().Add(s.ttl).Unix()
	encodedPaths := make([]string, len(paths))
	for i, p := range paths {
		// base64url the paths so the ':' and ',' separators can never appear
		// inside one, whatever a filename contains.
		encodedPaths[i] = base64.RawURLEncoding.EncodeToString([]byte(p.String()))
	}
	payload := "z:" + strconv.FormatInt(expires, 10) + ":" +
		strconv.FormatInt(userID, 10) + ":" + strings.Join(encodedPaths, ",")

	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.mac(encoded))
}

// VerifyBundle checks a bundle token and returns its paths and the account it
// was minted for. The "z:" marker keeps a bundle token from being read as a
// single-file one, and vice versa.
func (s *Signer) VerifyBundle(token string) ([]storage.Path, int64, error) {
	encoded, signature, found := strings.Cut(token, ".")
	if !found {
		return nil, 0, ErrInvalidSignature
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, 0, ErrInvalidSignature
	}
	if subtle.ConstantTimeCompare(provided, s.mac(encoded)) != 1 {
		return nil, 0, ErrInvalidSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, 0, ErrInvalidSignature
	}
	marker, rest, found := strings.Cut(string(payload), ":")
	if !found || marker != "z" {
		return nil, 0, ErrInvalidSignature
	}
	rawExpiry, rest, found := strings.Cut(rest, ":")
	if !found {
		return nil, 0, ErrInvalidSignature
	}
	rawUser, rawPaths, found := strings.Cut(rest, ":")
	if !found {
		return nil, 0, ErrInvalidSignature
	}
	expires, err := strconv.ParseInt(rawExpiry, 10, 64)
	if err != nil {
		return nil, 0, ErrInvalidSignature
	}
	userID, err := strconv.ParseInt(rawUser, 10, 64)
	if err != nil {
		return nil, 0, ErrInvalidSignature
	}
	if s.now().Unix() > expires {
		return nil, 0, ErrExpired
	}

	parts := strings.Split(rawPaths, ",")
	paths := make([]storage.Path, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return nil, 0, ErrInvalidSignature
		}
		p, err := storage.ParsePath(string(raw))
		if err != nil {
			return nil, 0, ErrInvalidSignature
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return nil, 0, ErrInvalidSignature
	}
	return paths, userID, nil
}

func (s *Signer) mac(encoded string) []byte {
	sum := hmac.New(sha256.New, s.key)
	sum.Write([]byte(encoded))
	return sum.Sum(nil)
}
