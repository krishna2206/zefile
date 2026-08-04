package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"

	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// RecoveryCodeCount is how many single-use codes a fresh set holds.
const RecoveryCodeCount = 10

// recoveryCodeChars is the length of the base32 body of a code (excluding the
// separator). Ten characters is 50 bits — beyond online guessing, and each code
// is Argon2-hashed, so a leaked database yields nothing brute-forceable either.
const recoveryCodeChars = 10

// ErrInvalidRecovery covers an unknown account and a wrong or spent code alike,
// so the reset form cannot be used to tell which usernames exist.
var ErrInvalidRecovery = errors.New("auth: invalid username or recovery code")

// recoveryEncoding is lowercase base32 without padding, dropping the letters
// that read ambiguously on paper (l, o, 0, 1).
var recoveryEncoding = base32.
	NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").
	WithPadding(base32.NoPadding)

// GenerateRecoveryCodes issues a fresh set of single-use codes for an account,
// replacing any it already had. The plaintext is returned once, to show the
// user; only Argon2 hashes are kept.
func (s *Service) GenerateRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	if err := s.writes.DeleteRecoveryCodesForUser(ctx, userID); err != nil {
		return nil, fmt.Errorf("auth: clear recovery codes: %w", err)
	}

	now := s.now().Unix()
	codes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		hash, err := HashPassword(code, s.params)
		if err != nil {
			return nil, err
		}
		if err := s.writes.CreateRecoveryCode(ctx, sqlcgen.CreateRecoveryCodeParams{
			UserID:    userID,
			CodeHash:  hash,
			CreatedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("auth: store recovery code: %w", err)
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// RecoveryCodesRemaining reports how many unused codes an account has left.
func (s *Service) RecoveryCodesRemaining(ctx context.Context, userID int64) (int, error) {
	n, err := s.reads.CountUnusedRecoveryCodesForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("auth: count recovery codes: %w", err)
	}
	return int(n), nil
}

// ResetPasswordWithCode sets a new password after spending a single-use recovery
// code. This is the emailless "forgot password": the code is the second factor
// that proves the request is genuine. On success every session is ended, so a
// forgotten — often compromised — password cannot outlive the reset.
func (s *Service) ResetPasswordWithCode(ctx context.Context, username, code, newPassword, address string) error {
	if address != "" && !s.byAddress.Allow(address) {
		return ErrRateLimited
	}
	if !s.byAccount.Allow(username) {
		return ErrRateLimited
	}

	username = NormaliseUsername(username)
	if err := ValidatePassword(newPassword, username); err != nil {
		return err
	}

	row, err := s.reads.GetUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("auth: look up account: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// Spend the same effort as a real verify, so an unknown account does not
		// answer measurably faster and become enumerable.
		_, _ = VerifyPassword(code, decoyHash())
		s.recordFailure(username, address)
		return ErrInvalidRecovery
	}

	ok, err := s.consumeRecoveryCode(ctx, row.ID, code)
	if err != nil {
		return err
	}
	if !ok {
		// Spend the same effort as a real verify, so a wrong code does not
		// answer measurably faster.
		_, _ = VerifyPassword(code, decoyHash())
		s.recordFailure(username, address)
		return ErrInvalidRecovery
	}

	hash, err := HashPassword(newPassword, s.params)
	if err != nil {
		return err
	}
	if err := s.writes.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{
		PasswordHash: hash,
		UpdatedAt:    s.now().Unix(),
		ID:           row.ID,
	}); err != nil {
		return fmt.Errorf("auth: set password: %w", err)
	}

	s.byAccount.Reset(username)
	if address != "" {
		s.byAddress.Reset(address)
	}
	return s.RevokeAllSessions(ctx, row.ID)
}

// consumeRecoveryCode spends a single-use code for an account, atomically: it
// returns true only if this call is the one that marked a matching code used, so
// two concurrent uses of the same code cannot both succeed. It reports false
// (not an error) when no unused code matches. The caller has already
// established the account, so there is no enumeration concern here.
func (s *Service) consumeRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	codes, err := s.reads.ListUnusedRecoveryCodesForUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("auth: load recovery codes: %w", err)
	}
	var matched int64
	for _, rc := range codes {
		if ok, err := VerifyPassword(code, rc.CodeHash); err == nil && ok {
			matched = rc.ID
			break
		}
	}
	if matched == 0 {
		return false, nil
	}
	spent, err := s.writes.MarkRecoveryCodeUsed(ctx, sqlcgen.MarkRecoveryCodeUsedParams{
		UsedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		ID:     matched,
	})
	if err != nil {
		return false, fmt.Errorf("auth: spend recovery code: %w", err)
	}
	return spent > 0, nil
}

// newRecoveryCode returns one human-typeable code like "abcde-fghij".
func newRecoveryCode() (string, error) {
	// base32 emits 8 characters per 5 bytes; 7 bytes yields at least ten.
	raw := make([]byte, 7)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate recovery code: %w", err)
	}
	body := recoveryEncoding.EncodeToString(raw)[:recoveryCodeChars]
	return body[:5] + "-" + body[5:], nil
}
