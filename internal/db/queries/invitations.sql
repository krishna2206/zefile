-- name: CreateInvitation :one
INSERT INTO invitations (token_hash, email, inviter_id, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetInvitationByTokenHash :one
-- Single use and expiry are filtered here, so a caller cannot accidentally
-- accept an invitation twice.
SELECT * FROM invitations
WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?;

-- name: MarkInvitationUsed :exec
UPDATE invitations SET used_at = ? WHERE id = ? AND used_at IS NULL;

-- name: DeleteUnusedInvitations :exec
DELETE FROM invitations WHERE used_at IS NULL AND inviter_id IS NULL;

-- name: ListPendingInvitations :many
-- Real invitations (those with an inviter) that are still open, newest first.
-- Setup tokens have no inviter and are excluded.
SELECT * FROM invitations
WHERE used_at IS NULL AND inviter_id IS NOT NULL AND expires_at > ?
ORDER BY created_at DESC;

-- name: DeleteInvitationByID :execrows
-- Only an unused, real invitation can be revoked; a used one has already become
-- an account, and a setup token is not an admin's to cancel.
DELETE FROM invitations WHERE id = ? AND used_at IS NULL AND inviter_id IS NOT NULL;
