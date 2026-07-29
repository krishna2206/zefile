// Package upload implements resumable uploads.
//
// The wire protocol is the core of tus: a session is opened, bytes arrive in
// PATCH requests carrying their offset, and a HEAD reports how far the server
// got. Following an existing protocol rather than inventing one means clients
// already exist in most languages, which is the same reason the API is treated
// as the product rather than as plumbing.
//
// Only the core is implemented, and deliberately: the concatenation, creation
// -with-upload and termination extensions add surface without changing what a
// forty-gigabyte transfer needs, which is to survive a dropped connection.
package upload

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/krishna2206/zefile/internal/auth"
	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
	"github.com/krishna2206/zefile/internal/storage"
)

// DefaultTTL is how long an untouched session survives.
//
// Long enough to span a laptop being closed overnight, short enough that
// abandoned partial files do not accumulate on a disk nobody is watching.
const DefaultTTL = 24 * time.Hour

// MaxUploadSize bounds a single transfer. It is generous because the whole
// point is disc images; the limit exists so a client cannot declare an absurd
// length and have the server reserve intent for it.
const MaxUploadSize = 1 << 42 // 4 TiB

// Errors returned by [Service].
var (
	// ErrNotFound means the session is unknown, expired, or already finished.
	ErrNotFound = errors.New("upload: no such session")

	// ErrOffsetMismatch means the client sent bytes for a position other than
	// the one the server is waiting at.
	ErrOffsetMismatch = errors.New("upload: offset does not match")

	// ErrSizeMismatch means the transfer ended at a length other than declared.
	ErrSizeMismatch = errors.New("upload: size does not match the declared length")

	// ErrChecksumMismatch means the assembled file does not hash to what the
	// client said it would.
	ErrChecksumMismatch = errors.New("upload: checksum does not match")

	// ErrTooLarge means the declared length exceeds what the instance accepts.
	ErrTooLarge = errors.New("upload: declared length is too large")
)

// Session is an upload in progress.
type Session struct {
	Token     string
	Path      storage.Path
	Size      int64
	Offset    int64
	ExpiresAt time.Time
	Complete  bool
}

// Service manages upload sessions.
type Service struct {
	fs     *storage.Local
	reads  *sqlcgen.Queries
	writes *sqlcgen.Queries
	ttl    time.Duration
	now    func() time.Time

	// Concurrent PATCHes against one session would interleave writes and
	// produce a file that is the wrong length in an unpredictable way. tus
	// forbids it; this makes the second request wait rather than trusting the
	// client to obey.
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Option adjusts a [Service].
type Option func(*Service)

// WithClock replaces the time source, for tests.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithTTL sets how long an untouched session survives.
func WithTTL(d time.Duration) Option { return func(s *Service) { s.ttl = d } }

// New builds a Service.
func New(database *db.DB, fs *storage.Local, opts ...Option) *Service {
	s := &Service{
		fs:     fs,
		reads:  sqlcgen.New(database.Read),
		writes: sqlcgen.New(database.Write),
		ttl:    DefaultTTL,
		now:    time.Now,
		locks:  make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create opens a session for a file of a declared length.
//
// Authorisation and free space are checked now, before the client spends an
// hour transferring: finding out at the end that the destination was never
// writable is the worst possible moment to learn it.
func (s *Service) Create(ctx context.Context, userID int64, target storage.Path, size int64, checksum string) (Session, error) {
	if size < 0 || size > MaxUploadSize {
		return Session{}, ErrTooLarge
	}
	if _, err := s.fs.Stat(ctx, target); err == nil {
		return Session{}, storage.ErrExist
	} else if !errors.Is(err, storage.ErrNotExist) {
		return Session{}, err
	}

	stage, err := s.fs.NewStage(ctx)
	if err != nil {
		return Session{}, err
	}

	token, _, err := auth.NewToken("zefile_up_")
	if err != nil {
		_ = s.fs.DiscardStage(ctx, stage)
		return Session{}, err
	}

	now := s.now()
	row, err := s.writes.CreateUpload(ctx, sqlcgen.CreateUploadParams{
		Token:      token,
		UserID:     userID,
		TargetPath: target.String(),
		Size:       size,
		StageID:    string(stage),
		Checksum:   nullableString(checksum),
		CreatedAt:  now.Unix(),
		ExpiresAt:  now.Add(s.ttl).Unix(),
	})
	if err != nil {
		_ = s.fs.DiscardStage(ctx, stage)
		return Session{}, fmt.Errorf("upload: create session: %w", err)
	}

	return s.toSession(row, 0), nil
}

// Offset reports how many bytes the server holds, which is where a client
// resumes from.
//
// It is read from the file rather than from the row: the file is what actually
// holds the bytes, and a row that drifted from it would have a client resume at
// the wrong place and produce a corrupt result.
func (s *Service) Offset(ctx context.Context, token string) (Session, error) {
	row, err := s.load(ctx, token)
	if err != nil {
		return Session{}, err
	}
	stage := storage.StageID(row.StageID)

	offset, err := s.fs.StageSize(ctx, stage)
	if err != nil {
		if errors.Is(err, storage.ErrNoStagedFile) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return s.toSession(row, offset), nil
}

// Write appends bytes at the given offset, committing the file once the
// declared length is reached.
func (s *Service) Write(ctx context.Context, token string, offset int64, body io.Reader) (Session, error) {
	unlock := s.lock(token)
	defer unlock()

	row, err := s.load(ctx, token)
	if err != nil {
		return Session{}, err
	}
	stage, declared := storage.StageID(row.StageID), row.Checksum.String

	current, err := s.fs.StageSize(ctx, stage)
	if err != nil {
		if errors.Is(err, storage.ErrNoStagedFile) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	if offset != current {
		// tus requires an exact match. Accepting anything else would let a
		// confused client silently produce a file with a hole in it.
		return Session{}, fmt.Errorf("%w: server is at %d, client sent %d", ErrOffsetMismatch, current, offset)
	}

	// Never accept more than was declared: without this an upload could grow
	// past the size the free-space check was made against.
	limited := io.LimitReader(body, row.Size-current)

	written, writeErr := s.fs.AppendToStage(ctx, stage, offset, limited)
	_ = s.writes.UpdateUploadOffset(ctx, sqlcgen.UpdateUploadOffsetParams{
		Received: written,
		ID:       row.ID,
	})
	if writeErr != nil {
		// The bytes that landed are kept. That is the entire point: the client
		// asks for the offset again and continues.
		return s.toSession(row, written), writeErr
	}

	if written < row.Size {
		return s.toSession(row, written), nil
	}
	if written > row.Size {
		return Session{}, ErrSizeMismatch
	}

	if err := s.finish(ctx, row, stage, declared); err != nil {
		return Session{}, err
	}
	session := s.toSession(row, written)
	session.Complete = true
	return session, nil
}

// finish verifies and publishes a completed upload.
func (s *Service) finish(ctx context.Context, row sqlcgen.Upload, stage storage.StageID, declared string) error {
	if declared != "" {
		got, err := s.hashStage(ctx, stage)
		if err != nil {
			return err
		}
		if got != declared {
			// The partial file is dropped: a transfer that did not arrive
			// intact is not something to leave lying around half-trusted.
			_ = s.fs.DiscardStage(ctx, stage)
			_ = s.writes.DeleteUpload(ctx, row.ID)
			return fmt.Errorf("%w: got %s, expected %s", ErrChecksumMismatch, got, declared)
		}
	}

	target, err := storage.ParsePath(row.TargetPath)
	if err != nil {
		return err
	}
	if err := s.fs.CommitStage(ctx, stage, target); err != nil {
		return err
	}
	return s.writes.DeleteUpload(ctx, row.ID)
}

// hashStage reads the staged file back to compute its digest.
//
// It costs a full sequential read, which is why a checksum is optional: on a
// forty-gigabyte transfer it is minutes of disk time. Clients that care about
// integrity ask for it; clients that do not are not charged for it.
func (s *Service) hashStage(ctx context.Context, stage storage.StageID) (string, error) {
	f, err := s.fs.OpenStage(ctx, stage)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", fmt.Errorf("upload: hash staged file: %w", err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// Cancel abandons a session and deletes what was received.
func (s *Service) Cancel(ctx context.Context, token string) error {
	row, err := s.load(ctx, token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // Cancelling twice is not an error.
		}
		return err
	}

	_ = s.fs.DiscardStage(ctx, storage.StageID(row.StageID))
	return s.writes.DeleteUpload(ctx, row.ID)
}

// PurgeExpired drops sessions past their expiry and the bytes behind them.
func (s *Service) PurgeExpired(ctx context.Context) (int, error) {
	rows, err := s.reads.ListExpiredUploads(ctx, s.now().Unix())
	if err != nil {
		return 0, fmt.Errorf("upload: list expired: %w", err)
	}
	for _, row := range rows {
		_ = s.fs.DiscardStage(ctx, storage.StageID(row.StageID))
		_ = s.writes.DeleteUpload(ctx, row.ID)
	}

	// Also sweep staged files with no session at all, which is what a crash
	// between creating the file and inserting its row leaves behind.
	orphans, err := s.fs.PurgeStagesOlderThan(ctx, s.now().Add(-2*s.ttl))
	if err != nil {
		return len(rows), err
	}
	return len(rows) + orphans, nil
}

func (s *Service) load(ctx context.Context, token string) (sqlcgen.Upload, error) {
	row, err := s.reads.GetUpload(ctx, sqlcgen.GetUploadParams{
		Token:     token,
		ExpiresAt: s.now().Unix(),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlcgen.Upload{}, ErrNotFound
		}
		return sqlcgen.Upload{}, fmt.Errorf("upload: load session: %w", err)
	}
	return row, nil
}

// lock serialises requests against one session and returns the release.
func (s *Service) lock(token string) func() {
	s.mu.Lock()
	m, ok := s.locks[token]
	if !ok {
		m = &sync.Mutex{}
		s.locks[token] = m
	}
	s.mu.Unlock()

	m.Lock()
	return func() {
		m.Unlock()
		// The map is swept rather than grown forever: a long-running instance
		// would otherwise keep a mutex per upload it has ever seen.
		s.mu.Lock()
		if len(s.locks) > 1024 {
			s.locks = make(map[string]*sync.Mutex)
		}
		s.mu.Unlock()
	}
}

func (s *Service) toSession(row sqlcgen.Upload, offset int64) Session {
	p, _ := storage.ParsePath(row.TargetPath)
	return Session{
		Token:     row.Token,
		Path:      p,
		Size:      row.Size,
		Offset:    offset,
		ExpiresAt: time.Unix(row.ExpiresAt, 0).UTC(),
	}
}

// nullableString stores an empty declared checksum as NULL rather than as an
// empty string, so "no checksum asked for" and "checksum is empty" cannot be
// confused by a later query.
func nullableString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}
