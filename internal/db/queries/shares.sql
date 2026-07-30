-- name: CreateShare :one
INSERT INTO shares (
    token_hash, owner_id, path, perms, password_hash,
    max_downloads, download_count, created_at, expires_at, revoked_at
)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, NULL)
RETURNING *;

-- name: GetShareByHash :one
-- The status (expired, revoked, exhausted) is decided in Go so the public
-- endpoint can tell a holder why a link stopped working; the row is fetched
-- whatever its state.
SELECT * FROM shares WHERE token_hash = ?;

-- name: ListSharesByOwner :many
SELECT * FROM shares
WHERE owner_id = ? AND revoked_at IS NULL
ORDER BY created_at DESC;

-- name: RevokeShare :execrows
UPDATE shares
SET revoked_at = ?
WHERE id = ? AND owner_id = ? AND revoked_at IS NULL;

-- name: IncrementShareDownloads :exec
UPDATE shares SET download_count = download_count + 1 WHERE id = ?;

-- name: LogShareAccess :exec
INSERT INTO share_access_log (share_id, at, ip, user_agent, bytes_sent)
VALUES (?, ?, ?, ?, ?);
