-- name: ListACLForUser :many
SELECT * FROM acl WHERE subject_type = 'user' AND subject_id = ?;

-- name: ListACLForGroups :many
SELECT * FROM acl
WHERE subject_type = 'group' AND subject_id IN (sqlc.slice('group_ids'));

-- name: ListGroupsForUser :many
SELECT group_id FROM group_members WHERE user_id = ?;

-- name: ListACLForPath :many
-- Backs the permissions screen: everything granted at one exact path.
SELECT * FROM acl WHERE path = ? ORDER BY subject_type, subject_id;

-- name: UpsertACL :one
INSERT INTO acl (subject_type, subject_id, path, perms, recursive, deny, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (subject_type, subject_id, path, deny)
DO UPDATE SET perms = excluded.perms, recursive = excluded.recursive
RETURNING *;

-- name: DeleteACL :exec
DELETE FROM acl WHERE id = ?;

-- name: DeleteACLForSubject :exec
DELETE FROM acl WHERE subject_type = ? AND subject_id = ?;
