-- name: CountUsers :one
-- Backs first-run detection: an instance with no account shows the setup link
-- rather than a sign-in form.
SELECT count(*) FROM users;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ? AND disabled = 0;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY username;

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, is_admin, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: SetUserAdmin :exec
UPDATE users SET is_admin = ?, updated_at = ? WHERE id = ?;

-- name: SetUserDisabled :exec
UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?;

-- name: DeleteUser :exec
-- Sessions and file ownership cascade with the row; ACL rules are keyed by a
-- subject type as well as an id, so they carry no foreign key and are cleared
-- separately by the caller.
DELETE FROM users WHERE id = ?;

-- name: SetTOTPSecret :exec
-- Sets or clears (NULL) the TOTP secret. A non-NULL secret means two-factor
-- authentication is enabled for the account.
UPDATE users SET totp_secret = ?, updated_at = ? WHERE id = ?;
