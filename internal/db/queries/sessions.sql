-- name: GetSessionByTokenHash :one
-- The lookup performed on every authenticated request. Expiry and revocation
-- are filtered here rather than in Go: a caller that forgets the check must not
-- be able to resurrect a dead session.
SELECT sqlc.embed(sessions), sqlc.embed(users)
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = ?
  AND sessions.revoked_at IS NULL
  AND sessions.expires_at > ?
  AND users.disabled = 0;

-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, created_at, last_seen_at, expires_at, user_agent, ip)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeAllSessionsForUser :exec
UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;

-- name: RevokeSessionForUser :execrows
-- Scoped to the owner so one account cannot end another's session by guessing
-- an id.
UPDATE sessions SET revoked_at = ?
WHERE id = ? AND user_id = ? AND revoked_at IS NULL;

-- name: RevokeOtherSessionsForUser :exec
-- Signs the account out everywhere but the session making the request.
UPDATE sessions SET revoked_at = ?
WHERE user_id = ? AND id != ? AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id = ?;

-- name: ListSessionsForUser :many
SELECT * FROM sessions
WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
ORDER BY last_seen_at DESC;
