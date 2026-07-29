-- name: CountUsers :one
-- Backs first-run detection: an instance with no account shows the setup link
-- rather than a sign-in form.
SELECT count(*) FROM users;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ? AND disabled = 0;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, is_admin, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;
