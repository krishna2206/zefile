package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- HMAC-SHA1 is the algorithm RFC 6238 defines for TOTP
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"

	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// Two-factor authentication uses TOTP (RFC 6238): the server and an
// authenticator app share a secret once, then each derives the same six-digit
// code from that secret and the current time. Verification is offline — no SMS,
// no email — which fits an instance that makes no outbound requests.

const (
	totpPeriod      = 30 // seconds per code
	totpDigits      = 6
	totpSecretBytes = 20 // 160-bit secret, the RFC 6238 recommendation

	// TOTPIssuer labels the account in the authenticator app.
	TOTPIssuer = "Zefile"
)

// totpEncoding is unpadded base32, the encoding authenticator apps expect for a
// provisioning secret.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh base32 secret for a new enrollment. It is
// not stored until the user proves they hold it by confirming a code.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate totp secret: %w", err)
	}
	return totpEncoding.EncodeToString(raw), nil
}

// TOTPProvisioningURI builds the otpauth:// URI an authenticator reads from a QR
// code. account is the username, shown alongside the issuer in the app.
func TOTPProvisioningURI(secret, account string) string {
	label := url.PathEscape(TOTPIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", TOTPIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// EnableTOTP turns two-factor auth on for an account, but only after code proves
// the user holds secret — so a secret is never stored unconfirmed.
func (s *Service) EnableTOTP(ctx context.Context, userID int64, secret, code string) error {
	if !s.validateTOTP(secret, code) {
		return ErrInvalidTOTP
	}
	return s.setTOTPSecret(ctx, userID, secret)
}

// DisableTOTP turns two-factor auth off after checking a current code, so a
// hijacked session cannot quietly remove the protection. A recovery code works
// too, for the case of a lost authenticator.
func (s *Service) DisableTOTP(ctx context.Context, userID int64, code string) error {
	secret, err := s.totpSecret(ctx, userID)
	if err != nil {
		return err
	}
	if secret == "" {
		return nil // already off
	}
	if !s.validateTOTP(secret, code) {
		ok, err := s.consumeRecoveryCode(ctx, userID, code)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidTOTP
		}
	}
	return s.setTOTPSecret(ctx, userID, "")
}

// VerifyLoginTOTP checks the second factor at sign-in, for an account that has
// it enabled. It accepts a current TOTP code, or a single-use recovery code
// which it spends — so a lost authenticator is a recovery, not a lockout.
func (s *Service) VerifyLoginTOTP(ctx context.Context, userID int64, code string) error {
	secret, err := s.totpSecret(ctx, userID)
	if err != nil {
		return err
	}
	if secret != "" && s.validateTOTP(secret, code) {
		return nil
	}
	ok, err := s.consumeRecoveryCode(ctx, userID, code)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return ErrInvalidTOTP
}

// validateTOTP reports whether code matches secret for the current time,
// tolerating one step of clock skew each way.
func (s *Service) validateTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	key, err := totpEncoding.DecodeString(strings.TrimSpace(secret))
	if err != nil {
		return false
	}
	counter := s.now().Unix() / totpPeriod
	for _, c := range []int64{counter - 1, counter, counter + 1} {
		if subtle.ConstantTimeCompare([]byte(totpCode(key, c)), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// totpCode is the RFC 6238 / HOTP derivation: HMAC-SHA1 of the counter, dynamic
// truncation to a 31-bit integer, reduced to the low digits.
func totpCode(key []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) // #nosec G115 -- counter is a positive time-derived step

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%0*d", totpDigits, value%1_000_000)
}

func (s *Service) totpSecret(ctx context.Context, userID int64) (string, error) {
	row, err := s.reads.GetUserByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("auth: look up account: %w", err)
	}
	if row.TotpSecret.Valid {
		return row.TotpSecret.String, nil
	}
	return "", nil
}

func (s *Service) setTOTPSecret(ctx context.Context, userID int64, secret string) error {
	value := sql.NullString{String: secret, Valid: secret != ""}
	if err := s.writes.SetTOTPSecret(ctx, sqlcgen.SetTOTPSecretParams{
		TotpSecret: value,
		UpdatedAt:  s.now().Unix(),
		ID:         userID,
	}); err != nil {
		return fmt.Errorf("auth: set totp secret: %w", err)
	}
	return nil
}
