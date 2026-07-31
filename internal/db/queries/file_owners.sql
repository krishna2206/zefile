-- name: GetFileOwner :one
SELECT * FROM file_owners WHERE path = ?;

-- name: GetFileOwnersForPaths :many
-- Batched so that listing a directory costs one query rather than one per entry.
SELECT * FROM file_owners WHERE path IN (sqlc.slice('paths'));

-- name: SetFileOwner :exec
INSERT INTO file_owners (path, owner_id, created_at) VALUES (?, ?, ?)
ON CONFLICT (path) DO UPDATE SET owner_id = excluded.owner_id;

-- name: MoveFileOwner :exec
-- Follows a rename across a whole subtree, so a moved folder keeps ownership of
-- everything inside it, not only its own row.
UPDATE file_owners
SET path = sqlc.arg(to_path) || substr(path, length(sqlc.arg(from_path)) + 1)
WHERE path = sqlc.arg(from_path)
   OR substr(path, 1, length(sqlc.arg(from_path)) + 1) = sqlc.arg(from_path) || '/';

-- name: DeleteFileOwner :exec
DELETE FROM file_owners WHERE path = ?;
