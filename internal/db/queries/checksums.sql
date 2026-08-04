-- name: GetChecksum :one
SELECT * FROM checksums WHERE path = ?;

-- name: UpsertChecksum :exec
INSERT INTO checksums (path, algo, hash, size, modified_at, computed_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    algo = excluded.algo, hash = excluded.hash, size = excluded.size,
    modified_at = excluded.modified_at, computed_at = excluded.computed_at;
