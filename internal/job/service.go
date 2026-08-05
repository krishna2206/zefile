// Package job runs long operations in the background.
//
// Some things a request cannot honestly do inside its lifetime: copying a
// directory tree, copying a very large file, and later reindexing for search.
// They are recorded as jobs in the database and executed by a single worker,
// which reports progress back onto the row so the interface can follow along.
//
// The queue is deliberately small. One worker, one table, at-least-once
// execution: a job interrupted by a crash is reset to pending on the next start
// and run again, so every handler must be safe to retry. It is enough for a
// self-hosted instance and adds no moving parts to deploy.
package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
)

// Type names a kind of job. Handlers register against it.
type Type string

const (
	// TypeCopy copies a file or directory tree in the background.
	TypeCopy Type = "copy"

	// TypeChecksum computes a file's SHA-256 in the background, so hashing a
	// very large file does not block a request.
	TypeChecksum Type = "checksum"

	// TypeExtract unpacks an archive in the background, since a large archive
	// takes longer than a request may honestly last.
	TypeExtract Type = "extract"

	// TypeFetch downloads a URL into storage from the server's own network,
	// which for a large file takes far longer than a request may last.
	TypeFetch Type = "fetch"
)

// Status values mirror the CHECK constraint on the jobs table.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// CopyPayload is the work a copy job carries. The user is recorded so the worker
// runs the copy with the same authority the caller had when they asked for it.
type CopyPayload struct {
	From    string `json:"from"`
	To      string `json:"to"`
	UserID  int64  `json:"user_id"`
	IsAdmin bool   `json:"is_admin"`
}

// ExtractPayload is the work an extraction job carries. Like a copy, the user
// is recorded so the worker unpacks with the same authority the caller had.
type ExtractPayload struct {
	Archive string `json:"archive"`
	Dest    string `json:"dest"`
	UserID  int64  `json:"user_id"`
	IsAdmin bool   `json:"is_admin"`
}

// FetchPayload is the work a download job carries. Dir is the folder the file
// lands in; Name, if empty, is derived from the URL. The user is recorded so
// the file is written with the caller's authority and owned by them.
type FetchPayload struct {
	URL     string `json:"url"`
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	UserID  int64  `json:"user_id"`
	IsAdmin bool   `json:"is_admin"`
}

// Job is a unit of background work as the interface sees it.
type Job struct {
	ID         int64
	Type       string
	Status     string
	Progress   float64
	Error      string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Handler runs one job. It reports progress as a fraction in [0,1]; the reports
// are throttled before they reach the database, so a handler may call report as
// often as it likes.
type Handler func(ctx context.Context, payload string, report func(fraction float64)) error

// Service is the queue and its worker.
type Service struct {
	reads    *sqlcgen.Queries
	writes   *sqlcgen.Queries
	handlers map[string]Handler
	now      func() time.Time
	poll     time.Duration
	wake     chan struct{}
	log      *slog.Logger
}

// Option adjusts a Service.
type Option func(*Service)

// WithClock replaces the time source, for tests.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithPollInterval sets how often the worker looks for work absent a nudge.
func WithPollInterval(d time.Duration) Option { return func(s *Service) { s.poll = d } }

// New builds a Service over the given database.
func New(database *db.DB, opts ...Option) *Service {
	s := &Service{
		reads:    sqlcgen.New(database.Read),
		writes:   sqlcgen.New(database.Write),
		handlers: make(map[string]Handler),
		now:      time.Now,
		poll:     2 * time.Second,
		wake:     make(chan struct{}, 1),
		log:      slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Register wires a handler for a job type. It must be called before Run.
func (s *Service) Register(t Type, h Handler) { s.handlers[string(t)] = h }

// Enqueue records a job and nudges the worker. The payload is stored as JSON.
func (s *Service) Enqueue(ctx context.Context, t Type, payload any) (Job, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Job{}, fmt.Errorf("job: encode payload: %w", err)
	}
	row, err := s.writes.CreateJob(ctx, sqlcgen.CreateJobParams{
		Type:      string(t),
		Payload:   string(encoded),
		CreatedAt: s.now().Unix(),
	})
	if err != nil {
		return Job{}, fmt.Errorf("job: create: %w", err)
	}
	s.nudge()
	return toJob(row), nil
}

// Get returns one job by id.
func (s *Service) Get(ctx context.Context, id int64) (Job, error) {
	row, err := s.reads.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, err
	}
	return toJob(row), nil
}

// List returns the most recent jobs, newest first.
func (s *Service) List(ctx context.Context, limit int64) ([]Job, error) {
	rows, err := s.reads.ListRecentJobs(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, toJob(r))
	}
	return out, nil
}

// ErrNotFound means no job has the given id.
var ErrNotFound = errors.New("job: no such job")

// Run works the queue until the context is cancelled.
//
// Jobs left running by a previous crash are reset first, then the worker drains
// everything pending and sleeps until nudged or the poll interval elapses.
func (s *Service) Run(ctx context.Context) {
	if err := s.writes.RequeueRunningJobs(ctx); err != nil {
		s.log.Warn("job: could not requeue interrupted jobs", "error", err)
	}

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	for {
		for s.step(ctx) {
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

// step claims and runs one job, returning whether one was found.
func (s *Service) step(ctx context.Context) bool {
	row, err := s.writes.ClaimNextJob(ctx, sql.NullInt64{Int64: s.now().Unix(), Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		s.log.Warn("job: claim failed", "error", err)
		return false
	}

	handler, ok := s.handlers[row.Type]
	if !ok {
		s.finish(ctx, row.ID, StatusFailed, 0, "unknown job type "+row.Type)
		return true
	}

	report := s.throttledReporter(ctx, row.ID)
	if err := handler(ctx, row.Payload, report); err != nil {
		// A cancelled context is a shutdown, not a failure: leave the job so the
		// next start requeues and retries it.
		if ctx.Err() != nil {
			return false
		}
		s.log.Warn("job: handler failed", "id", row.ID, "type", row.Type, "error", err)
		s.finish(ctx, row.ID, StatusFailed, 0, err.Error())
		return true
	}
	s.finish(ctx, row.ID, StatusDone, 1, "")
	return true
}

// throttledReporter returns a progress callback that writes to the database at
// most a few times a second, so a fast copy does not flood the writer.
func (s *Service) throttledReporter(ctx context.Context, id int64) func(float64) {
	var last time.Time
	return func(fraction float64) {
		now := s.now()
		if now.Sub(last) < 400*time.Millisecond {
			return
		}
		last = now
		if fraction < 0 {
			fraction = 0
		} else if fraction > 1 {
			fraction = 1
		}
		if err := s.writes.UpdateJobProgress(ctx, sqlcgen.UpdateJobProgressParams{Progress: fraction, ID: id}); err != nil {
			s.log.Warn("job: progress update failed", "id", id, "error", err)
		}
	}
}

func (s *Service) finish(ctx context.Context, id int64, status string, progress float64, errMsg string) {
	var e sql.NullString
	if errMsg != "" {
		e = sql.NullString{String: errMsg, Valid: true}
	}
	if err := s.writes.FinishJob(ctx, sqlcgen.FinishJobParams{
		Status:     status,
		Progress:   progress,
		Error:      e,
		FinishedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		ID:         id,
	}); err != nil {
		s.log.Warn("job: finish failed", "id", id, "error", err)
	}
}

// nudge wakes the worker without blocking if it is already awake.
func (s *Service) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func toJob(r sqlcgen.Job) Job {
	j := Job{
		ID:        r.ID,
		Type:      r.Type,
		Status:    r.Status,
		Progress:  r.Progress,
		Error:     r.Error.String,
		CreatedAt: time.Unix(r.CreatedAt, 0),
	}
	if r.StartedAt.Valid {
		j.StartedAt = time.Unix(r.StartedAt.Int64, 0)
	}
	if r.FinishedAt.Valid {
		j.FinishedAt = time.Unix(r.FinishedAt.Int64, 0)
	}
	return j
}
