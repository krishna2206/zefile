-- name: GetFileOwner :one
SELECT * FROM file_owners WHERE path = ?;

-- name: GetFileOwnersForPaths :many
-- Batched so that listing a directory costs one query rather than one per entry.
SELECT * FROM file_owners WHERE path IN (sqlc.slice('paths'));

-- name: SetFileOwner :exec
INSERT INTO file_owners (path, owner_id, created_at) VALUES (?, ?, ?)
ON CONFLICT (path) DO UPDATE SET owner_id = excluded.owner_id;

-- name: MoveFileOwner :exec
UPDATE file_owners SET path = ? WHERE path = ?;

-- name: DeleteFileOwner :exec
DELETE FROM file_owners WHERE path = ?;
