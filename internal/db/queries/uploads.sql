-- name: CreateUpload :one
INSERT INTO uploads (token, user_id, target_path, size, received, stage_id, checksum, created_at, expires_at)
VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?)
RETURNING *;

-- name: GetUpload :one
-- Expiry is filtered here so an abandoned session cannot be revived days later
-- by a client that kept its token.
SELECT * FROM uploads WHERE token = ? AND expires_at > ?;

-- name: UpdateUploadOffset :exec
UPDATE uploads SET received = ? WHERE id = ?;

-- name: DeleteUpload :exec
DELETE FROM uploads WHERE id = ?;

-- name: ListExpiredUploads :many
SELECT * FROM uploads WHERE expires_at <= ?;
