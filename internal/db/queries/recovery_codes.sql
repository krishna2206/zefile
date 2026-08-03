-- name: DeleteRecoveryCodesForUser :exec
-- Regenerating replaces the whole set, so the old codes are cleared first.
DELETE FROM recovery_codes WHERE user_id = ?;

-- name: CreateRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?);

-- name: ListUnusedRecoveryCodesForUser :many
-- A reset tries the presented code against each unused code for the account.
SELECT id, code_hash FROM recovery_codes
WHERE user_id = ? AND used_at IS NULL;

-- name: CountUnusedRecoveryCodesForUser :one
SELECT count(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL;

-- name: MarkRecoveryCodeUsed :execrows
-- Scoped to an unused code so the same code can never be spent twice, even in a
-- race: execrows lets the caller confirm exactly one row changed.
UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL;
