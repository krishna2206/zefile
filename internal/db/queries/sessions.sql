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

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ?;
