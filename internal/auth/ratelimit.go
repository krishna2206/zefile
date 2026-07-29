package auth

import (
	"sync"
	"time"
)

// Limiter throttles repeated failures against a key.
//
// It counts only failures, never successes: someone signing in correctly from
// a busy office should not be pushed towards a lockout by their colleagues.
//
// State is in memory and does not survive a restart. That is a deliberate
// trade — persisting it would mean a database write on every failed attempt,
// turning the login endpoint into a way to make the server do work. An attacker
// gains a fresh budget when the process restarts, which is not something they
// can trigger.
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	failures int
	resetAt  time.Time
}

// NewLimiter allows limit failures per key within window.
//
// now may be nil, meaning the wall clock. Taking it here rather than exposing a
// settable field keeps the clock from being swapped while the limiter is
// already in use.
func NewLimiter(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		limit:   limit,
		window:  window,
		now:     now,
		entries: make(map[string]*entry),
	}
}

// Allow reports whether an attempt against key may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok || l.now().After(e.resetAt) {
		return true
	}
	return e.failures < l.limit
}

// Fail records a failed attempt and reports whether the key is now blocked.
func (l *Limiter) Fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	e, ok := l.entries[key]
	if !ok || now.After(e.resetAt) {
		e = &entry{resetAt: now.Add(l.window)}
		l.entries[key] = e
	}
	e.failures++

	// Sweeping here rather than from a goroutine keeps the limiter free of
	// lifecycle: nothing to start, nothing to stop, nothing to leak.
	l.sweepLocked(now)

	return e.failures >= l.limit
}

// Reset clears the record for a key, called after a successful sign-in so a
// legitimate user is not still throttled by their own earlier typos.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// RetryAfter reports how long the key stays blocked, or zero if it is not.
func (l *Limiter) RetryAfter(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok || e.failures < l.limit {
		return 0
	}
	if remaining := e.resetAt.Sub(l.now()); remaining > 0 {
		return remaining
	}
	return 0
}

// sweepLocked drops expired entries. The map is bounded by the number of
// distinct keys seen within one window, so an attacker cycling addresses cannot
// grow it without bound.
func (l *Limiter) sweepLocked(now time.Time) {
	const sweepThreshold = 1024
	if len(l.entries) < sweepThreshold {
		return
	}
	for key, e := range l.entries {
		if now.After(e.resetAt) {
			delete(l.entries, key)
		}
	}
}
