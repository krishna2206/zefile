-- name: CreateTrash :one
INSERT INTO trash (trash_name, original_path, deleted_by, deleted_at, is_dir)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: ListTrash :many
-- Most recently deleted first: that is the order someone reaches for the trash
-- to undo a mistake they just made.
SELECT * FROM trash ORDER BY deleted_at DESC;

-- name: GetTrash :one
SELECT * FROM trash WHERE id = ?;

-- name: DeleteTrash :exec
DELETE FROM trash WHERE id = ?;
