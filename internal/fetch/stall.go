package fetch

import (
	"context"
	"io"
	"sync"
	"time"
)

// stallReader wraps a response body with a watchdog: if no bytes arrive within
// the timeout, it cancels the request context, which unblocks the pending Read
// with an error. It is how a wedged connection is abandoned without imposing a
// total deadline on an honest, slow, very large transfer.
type stallReader struct {
	r       io.ReadCloser
	cancel  context.CancelFunc
	timeout time.Duration

	mu    sync.Mutex
	timer *time.Timer
	done  bool
}

func newStallReader(r io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) *stallReader {
	s := &stallReader{r: r, cancel: cancel, timeout: timeout}
	s.timer = time.AfterFunc(timeout, cancel)
	return s
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.mu.Lock()
		if !s.done {
			s.timer.Reset(s.timeout)
		}
		s.mu.Unlock()
	}
	return n, err
}

// Close stops the watchdog, cancels the request context — releasing the
// connection — and closes the underlying body. It is idempotent.
func (s *stallReader) Close() error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	s.timer.Stop()
	s.mu.Unlock()

	s.cancel()
	return s.r.Close()
}
