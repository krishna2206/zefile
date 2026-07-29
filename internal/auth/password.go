package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the Argon2id cost parameters.
//
// The defaults follow OWASP's low-memory recommendation rather than its
// high-memory one. Zefile runs on modest self-hosted machines, and since each
// concurrent sign-in allocates Memory bytes, a 64 MiB setting would turn the
// login endpoint into a way to exhaust RAM. Rate limiting narrows that, but
// choosing a parameter set that is cheap to defend is better than relying on
// the limiter alone.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams is the parameter set used for new hashes.
var DefaultParams = Params{
	Memory:      19 * 1024, // 19 MiB
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// Errors returned when handling password hashes.
var (
	// ErrInvalidHash means the stored string is not a hash this package wrote.
	ErrInvalidHash = errors.New("auth: malformed password hash")

	// ErrIncompatibleVersion means the hash was produced by a newer Argon2
	// version than this build understands.
	ErrIncompatibleVersion = errors.New("auth: incompatible argon2 version")
)

// HashPassword derives a hash in PHC string format.
//
// The format embeds the parameters, so raising the cost later does not
// invalidate existing hashes: they keep verifying under their own settings,
// and [NeedsRehash] reports which ones to upgrade at the next sign-in.
func HashPassword(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches encoded.
//
// A malformed hash yields an error, never a match. The comparison is constant
// time so that a wrong password cannot be narrowed down by timing.
func VerifyPassword(password, encoded string) (bool, error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether a stored hash uses weaker parameters than the
// given ones, meaning it should be replaced the next time the password is
// known — which is only during a successful sign-in.
func NeedsRehash(encoded string, want Params) bool {
	got, _, _, err := decodeHash(encoded)
	if err != nil {
		// Unreadable hashes cannot be verified against either, so the account
		// is already broken; reporting true at least routes it to the code
		// that would replace it.
		return true
	}
	return got.Memory < want.Memory ||
		got.Iterations < want.Iterations ||
		got.KeyLength < want.KeyLength
}

func decodeHash(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return p, nil, nil, ErrIncompatibleVersion
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, ErrInvalidHash
	}

	if salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil {
		return p, nil, nil, ErrInvalidHash
	}
	if key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil {
		return p, nil, nil, ErrInvalidHash
	}

	if len(salt) == 0 || len(key) == 0 {
		return p, nil, nil, ErrInvalidHash
	}

	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))
	return p, salt, key, nil
}
