-- name: CreateGroup :one
INSERT INTO groups (name, created_at) VALUES (?, ?) RETURNING *;

-- name: ListGroups :many
-- Groups with how many members each holds, for the management screen.
SELECT g.id, g.name, g.created_at, COUNT(gm.user_id) AS member_count
FROM groups g
LEFT JOIN group_members gm ON gm.group_id = g.id
GROUP BY g.id
ORDER BY g.name;

-- name: GroupExists :one
SELECT count(*) FROM groups WHERE id = ?;

-- name: DeleteGroup :execrows
DELETE FROM groups WHERE id = ?;

-- name: AddGroupMember :exec
INSERT INTO group_members (group_id, user_id) VALUES (?, ?)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :exec
DELETE FROM group_members WHERE group_id = ? AND user_id = ?;

-- name: ListGroupMemberIDs :many
SELECT user_id FROM group_members WHERE group_id = ?;
