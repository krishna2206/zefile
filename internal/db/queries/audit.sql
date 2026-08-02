-- name: InsertAuditEntry :exec
INSERT INTO audit_log (at, actor_id, actor_ip, action, target, details)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAuditEntries :many
-- Newest first, keyset-paginated by id (strictly decreasing). Pass a large id
-- for the first page. The actor's name is joined in; it is null when the account
-- was since deleted, which the entry still records by id and ip.
SELECT a.id, a.at, a.actor_id, a.actor_ip, a.action, a.target, a.details, u.username AS actor_username
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE a.id < ?
ORDER BY a.id DESC
LIMIT ?;
