package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// TokenBytes is the entropy behind every generated token.
//
// 256 bits is far beyond guessing. It matters most for share links, which are
// pure capabilities: whoever holds one has access, so the only defence is that
// the value cannot be found by trying.
const TokenBytes = 32

// Token prefixes. An explicit, searchable prefix means a leaked token is
// recognisable in a repository, a log file or a paste — secret scanners look
// for exactly this shape.
const (
	SessionPrefix = "zefile_sess_"
	APIPrefix     = "zefile_live_"
	InvitePrefix  = "zefile_inv_"
	SharePrefix   = "zefile_shr_"
)

// NewToken returns a fresh token and the hash to store for it.
//
// The plaintext is returned once and never persisted. Everything the database
// holds is the hash, so a database leak yields nothing that can be presented
// as a credential.
func NewToken(prefix string) (token string, hash []byte, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: generate token: %w", err)
	}

	token = prefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken returns the stored form of a token.
//
// SHA-256 rather than a password hash, deliberately. Argon2 exists to make
// guessing a low-entropy human secret expensive; a 256-bit random token has
// nothing to guess. What matters here instead is that lookup stays a single
// indexed query on every authenticated request, which a deliberately slow hash
// would ruin.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// TokenDisplayPrefix returns the leading fragment shown in the interface so a
// token can be recognised without revealing it. Tokens are displayed once, at
// creation; afterwards only this remains.
func TokenDisplayPrefix(token string) string {
	const shown = 8

	name, value, found := strings.Cut(token, "_")
	if !found {
		if len(token) <= shown {
			return token
		}
		return token[:shown]
	}
	// Keep the full namespace, then a slice of the random part.
	head := name + "_"
	if kind, rest, ok := strings.Cut(value, "_"); ok {
		head += kind + "_"
		value = rest
	}
	if len(value) > shown {
		value = value[:shown]
	}
	return head + value
}
