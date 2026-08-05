-- name: CreateJob :one
INSERT INTO jobs (type, payload, status, created_at)
VALUES (?, ?, 'pending', ?)
RETURNING *;

-- name: ClaimNextJob :one
-- Atomically take the oldest runnable job and mark it running. The single writer
-- connection serialises this, so two workers could not claim the same row. A
-- paused job keeps status 'pending' but is skipped until its flag is cleared.
UPDATE jobs
SET status = 'running', started_at = ?
WHERE id = (
    SELECT id FROM jobs
    WHERE status = 'pending' AND paused = 0
    ORDER BY created_at, id LIMIT 1
)
RETURNING *;

-- name: UpdateJobProgress :exec
UPDATE jobs SET progress = ?, bytes_done = ?, bytes_total = ? WHERE id = ?;

-- name: FinishJob :exec
UPDATE jobs SET status = ?, progress = ?, error = ?, finished_at = ? WHERE id = ?;

-- name: MarkJobPaused :exec
-- Return a running job to a paused state: it leaves the worker but keeps its
-- progress and any staged bytes so it can be resumed from where it stopped.
UPDATE jobs
SET status = 'pending', paused = 1, started_at = NULL
WHERE id = ? AND status = 'running';

-- name: ResumeJob :exec
-- Clear the pause flag so the worker picks the job up again.
UPDATE jobs SET paused = 0 WHERE id = ? AND paused = 1;

-- name: CancelPendingJob :exec
-- Cancel a job that is not currently running (pending or paused, both of which
-- keep status 'pending'). A running job is cancelled by signalling its worker.
UPDATE jobs
SET status = 'cancelled', paused = 0, finished_at = ?
WHERE id = ? AND status = 'pending';

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: ListRecentJobs :many
SELECT * FROM jobs ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: RequeueRunningJobs :exec
-- A job left 'running' by a crash is reset so the worker picks it up again on
-- the next start; its own idempotent construction makes the retry safe.
UPDATE jobs SET status = 'pending', started_at = NULL WHERE status = 'running';
