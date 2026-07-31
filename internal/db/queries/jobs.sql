-- name: CreateJob :one
INSERT INTO jobs (type, payload, status, created_at)
VALUES (?, ?, 'pending', ?)
RETURNING *;

-- name: ClaimNextJob :one
-- Atomically take the oldest pending job and mark it running. The single writer
-- connection serialises this, so two workers could not claim the same row.
UPDATE jobs
SET status = 'running', started_at = ?
WHERE id = (SELECT id FROM jobs WHERE status = 'pending' ORDER BY created_at, id LIMIT 1)
RETURNING *;

-- name: UpdateJobProgress :exec
UPDATE jobs SET progress = ? WHERE id = ?;

-- name: FinishJob :exec
UPDATE jobs SET status = ?, progress = ?, error = ?, finished_at = ? WHERE id = ?;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: ListRecentJobs :many
SELECT * FROM jobs ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: RequeueRunningJobs :exec
-- A job left 'running' by a crash is reset so the worker picks it up again on
-- the next start; its own idempotent construction makes the retry safe.
UPDATE jobs SET status = 'pending', started_at = NULL WHERE status = 'running';
