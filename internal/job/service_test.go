package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/krishna2206/zefile/internal/db"
)

func newService(t *testing.T) *Service {
	t.Helper()
	database, err := db.Open(t.Context(), db.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, WithPollInterval(20*time.Millisecond))
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func status(t *testing.T, s *Service, id int64) string {
	t.Helper()
	j, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	return j.Status
}

func TestCancelRunningJob(t *testing.T) {
	t.Parallel()
	s := newService(t)

	started := make(chan struct{})
	var sawCause error
	s.Register("blocker", func(ctx context.Context, _ string, _ func(done, total int64)) error {
		close(started)
		<-ctx.Done()
		sawCause = context.Cause(ctx)
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	j, err := s.Enqueue(context.Background(), "blocker", struct{}{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started

	if err := s.Cancel(context.Background(), j.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitFor(t, func() bool { return status(t, s, j.ID) == StatusCancelled })
	if !errors.Is(sawCause, ErrCancelled) {
		t.Errorf("handler saw cause %v, want ErrCancelled", sawCause)
	}
}

func TestPauseAndResumeJob(t *testing.T) {
	t.Parallel()
	s := newService(t)

	runs := make(chan struct{}, 4)
	release := make(chan struct{})
	s.Register("resumable", func(ctx context.Context, _ string, _ func(done, total int64)) error {
		runs <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil // completes on the resumed run
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	j, err := s.Enqueue(context.Background(), "resumable", struct{}{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-runs // first run started

	if err := s.Pause(context.Background(), j.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitFor(t, func() bool { return status(t, s, j.ID) == StatusPaused })

	// A paused job is not re-claimed by the worker until it is resumed.
	select {
	case <-runs:
		t.Fatal("paused job was run again before resume")
	case <-time.After(150 * time.Millisecond):
	}

	if err := s.Resume(context.Background(), j.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	<-runs // resumed run started
	close(release)
	waitFor(t, func() bool { return status(t, s, j.ID) == StatusDone })
}

func TestPauseRejectsNonRunningJob(t *testing.T) {
	t.Parallel()
	s := newService(t)
	// A pending job with no worker running: it is not running, so pause refuses.
	j, err := s.Enqueue(context.Background(), "never", struct{}{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := s.Pause(context.Background(), j.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("pause err = %v, want ErrNotRunning", err)
	}
}

func TestCancelPendingJob(t *testing.T) {
	t.Parallel()
	s := newService(t)
	// No worker: the job stays pending, and cancel marks it cancelled directly.
	j, err := s.Enqueue(context.Background(), "never", struct{}{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := s.Cancel(context.Background(), j.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := status(t, s, j.ID); got != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", got)
	}
}
