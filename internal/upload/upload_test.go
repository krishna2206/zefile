package upload_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/storage"
	"github.com/krishna2206/zefile/internal/upload"
)

type harness struct {
	svc  *upload.Service
	fs   *storage.Local
	root string
}

func newHarness(t *testing.T, opts ...upload.Option) *harness {
	t.Helper()

	database, err := db.Open(t.Context(), db.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Write.ExecContext(t.Context(),
		`INSERT INTO users (id, username, password_hash, created_at, updated_at)
		 VALUES (1, 'krishna', 'x', 0, 0)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	root := t.TempDir()
	fs, err := storage.Open(storage.Config{Root: root, Reserve: 1})
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	return &harness{svc: upload.New(database, fs, opts...), fs: fs, root: root}
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestInterruptedUploadResumes is the completion criterion for this lot: a
// transfer cut in the middle, resumed, and producing a file whose checksum is
// correct.
func TestInterruptedUploadResumes(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	payload := make([]byte, 3<<20)
	for i := range payload {
		payload[i] = byte(i * 7 % 251)
	}
	target := storage.MustParsePath("/jeu.iso")

	session, err := h.svc.Create(ctx, 1, target, int64(len(payload)), digest(payload))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First half arrives.
	half := len(payload) / 2
	if _, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader(payload[:half])); err != nil {
		t.Fatalf("first chunk: %v", err)
	}

	// The connection drops. The client comes back and asks where it got to —
	// which is read from the file itself, not from a counter that could drift.
	resumed, err := h.svc.Offset(ctx, session.Token)
	if err != nil {
		t.Fatalf("Offset: %v", err)
	}
	if resumed.Offset != int64(half) {
		t.Fatalf("Offset = %d, want %d", resumed.Offset, half)
	}
	if _, err := os.Stat(filepath.Join(h.root, "jeu.iso")); !os.IsNotExist(err) {
		t.Fatal("a partial upload is already visible at its destination")
	}

	final, err := h.svc.Write(ctx, session.Token, resumed.Offset, bytes.NewReader(payload[half:]))
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	if !final.Complete {
		t.Fatal("the upload did not complete after the declared length was reached")
	}

	got, err := os.ReadFile(filepath.Join(h.root, "jeu.iso"))
	if err != nil {
		t.Fatalf("read the result: %v", err)
	}
	if digest(got) != digest(payload) {
		t.Fatalf("the reassembled file differs: %d bytes vs %d", len(got), len(payload))
	}
}

func TestManySmallChunks(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	payload := bytes.Repeat([]byte("abcdefgh"), 4096)
	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/data.bin"), int64(len(payload)), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const chunk = 1000
	offset := int64(0)
	for offset < int64(len(payload)) {
		end := min(offset+chunk, int64(len(payload)))
		s, err := h.svc.Write(ctx, session.Token, offset, bytes.NewReader(payload[offset:end]))
		if err != nil {
			t.Fatalf("chunk at %d: %v", offset, err)
		}
		offset = s.Offset
	}

	got, err := os.ReadFile(filepath.Join(h.root, "data.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("the file assembled from many chunks does not match")
	}
}

func TestOffsetMismatchIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	payload := []byte("0123456789")
	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/a.bin"), int64(len(payload)), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader(payload[:4])); err != nil {
		t.Fatalf("first chunk: %v", err)
	}

	// Sending at the wrong position would leave a hole no checksum could catch.
	for _, offset := range []int64{0, 2, 6, 100} {
		if _, err := h.svc.Write(ctx, session.Token, offset, bytes.NewReader(payload[4:])); !errors.Is(err, upload.ErrOffsetMismatch) {
			t.Errorf("offset %d = %v, want ErrOffsetMismatch", offset, err)
		}
	}
	if _, err := h.svc.Write(ctx, session.Token, 4, bytes.NewReader(payload[4:])); err != nil {
		t.Errorf("the correct offset was refused: %v", err)
	}
}

func TestChecksumMismatchDiscardsEverything(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	payload := []byte("the bytes that actually arrive")
	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/a.bin"),
		int64(len(payload)), digest([]byte("something else entirely")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = h.svc.Write(ctx, session.Token, 0, bytes.NewReader(payload))
	if !errors.Is(err, upload.ErrChecksumMismatch) {
		t.Fatalf("Write = %v, want ErrChecksumMismatch", err)
	}

	// Nothing is published, and nothing is left behind half-trusted.
	if _, err := os.Stat(filepath.Join(h.root, "a.bin")); !os.IsNotExist(err) {
		t.Error("a file that failed its checksum was published anyway")
	}
	if _, err := h.svc.Offset(ctx, session.Token); !errors.Is(err, upload.ErrNotFound) {
		t.Error("the session survived a checksum failure")
	}
}

func TestUploadRefusesToOverwrite(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.root, "existing.bin"), []byte("old"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := h.svc.Create(t.Context(), 1, storage.MustParsePath("/existing.bin"), 10, "")
	if !errors.Is(err, storage.ErrExist) {
		t.Fatalf("Create = %v, want ErrExist", err)
	}
}

// TestExtraBytesAreIgnored: a client sending more than it declared must not be
// able to grow the file past the size the free-space check was made against.
func TestExtraBytesAreIgnored(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	declared := []byte("exactly ten")
	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/a.bin"), int64(len(declared)), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	toSend := append(append([]byte{}, declared...), bytes.Repeat([]byte("x"), 1000)...)
	final, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader(toSend))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !final.Complete || final.Offset != int64(len(declared)) {
		t.Fatalf("offset = %d, want %d", final.Offset, len(declared))
	}

	got, err := os.ReadFile(filepath.Join(h.root, "a.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, declared) {
		t.Fatalf("file is %q, want %q", got, declared)
	}
}

func TestCancelRemovesEverything(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/a.bin"), 100, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader(bytes.Repeat([]byte("x"), 50))); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := h.svc.Cancel(ctx, session.Token); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := h.svc.Offset(ctx, session.Token); !errors.Is(err, upload.ErrNotFound) {
		t.Error("the session survived cancellation")
	}
	// Cancelling twice is not an error: a client retrying after a dropped
	// response must not see a failure.
	if err := h.svc.Cancel(ctx, session.Token); err != nil {
		t.Errorf("second Cancel: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(h.root, storage.UploadsDir))
	if err == nil && len(entries) != 0 {
		t.Errorf("%d staged files left after cancellation", len(entries))
	}
}

func TestExpiredSessionsArePurged(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	h := newHarness(t,
		upload.WithTTL(time.Hour),
		upload.WithClock(func() time.Time { return clock }),
	)
	ctx := t.Context()

	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/a.bin"), 100, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader([]byte("partial"))); err != nil {
		t.Fatalf("Write: %v", err)
	}

	clock = clock.Add(2 * time.Hour)

	// An expired session stops working before anything is purged: expiry is
	// enforced by the query, not by the sweep having run.
	if _, err := h.svc.Offset(ctx, session.Token); !errors.Is(err, upload.ErrNotFound) {
		t.Errorf("an expired session still resolved: %v", err)
	}

	purged, err := h.svc.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if purged == 0 {
		t.Error("PurgeExpired removed nothing")
	}
}

// TestPartialFileIsNeverVisible covers the reason for staging: an upload in
// flight must not appear in a listing, and must not be reachable by name.
func TestPartialFileIsNeverVisible(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/gros.iso"), 1000, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader(bytes.Repeat([]byte("x"), 500))); err != nil {
		t.Fatalf("Write: %v", err)
	}

	entries, err := h.fs.List(ctx, storage.Root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("listing shows %d entries during an upload, want none", len(entries))
	}
	if _, err := h.fs.Stat(ctx, storage.MustParsePath("/gros.iso")); !errors.Is(err, storage.ErrNotExist) {
		t.Errorf("the destination exists mid-upload: %v", err)
	}
}

// TestConcurrentWritesAreSerialised: tus forbids parallel chunks on one
// session. Rather than trust a client to obey, the second request waits.
func TestConcurrentWritesAreSerialised(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	session, err := h.svc.Create(ctx, 1, storage.MustParsePath("/a.bin"), 2000, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := h.svc.Write(ctx, session.Token, 0, bytes.NewReader(bytes.Repeat([]byte("y"), 1000)))
			done <- err
		}()
	}

	var succeeded, rejected int
	for range 2 {
		if err := <-done; err == nil {
			succeeded++
		} else if errors.Is(err, upload.ErrOffsetMismatch) {
			rejected++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// Exactly one wins; the loser is told the offset moved rather than
	// interleaving its bytes with the winner's.
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("%d succeeded and %d were rejected, want one of each", succeeded, rejected)
	}

	offset, err := h.svc.Offset(ctx, session.Token)
	if err != nil {
		t.Fatalf("Offset: %v", err)
	}
	if offset.Offset != 1000 {
		t.Fatalf("offset = %d, want 1000 — the writes interleaved", offset.Offset)
	}
}

// TestCancelledRequestKeepsWhatArrived is what makes an upload resumable
// rather than merely restartable.
func TestCancelledRequestKeepsWhatArrived(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	session, err := h.svc.Create(t.Context(), 1, storage.MustParsePath("/a.bin"), 1<<20, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancellingReader{after: 4096, cancel: cancel, src: bytes.NewReader(bytes.Repeat([]byte("z"), 1<<20))}

	if _, err := h.svc.Write(ctx, session.Token, 0, reader); err == nil {
		t.Fatal("a cancelled write reported success")
	}

	resumed, err := h.svc.Offset(t.Context(), session.Token)
	if err != nil {
		t.Fatalf("Offset after cancellation: %v", err)
	}
	if resumed.Offset == 0 {
		t.Fatal("everything received before the cancellation was thrown away")
	}
	if resumed.Offset >= 1<<20 {
		t.Fatalf("offset = %d, want a partial transfer", resumed.Offset)
	}
}

// cancellingReader cancels the context part way through, standing in for a
// client whose connection drops.
type cancellingReader struct {
	after  int
	read   int
	cancel context.CancelFunc
	src    io.Reader
}

func (c *cancellingReader) Read(p []byte) (int, error) {
	if c.read >= c.after {
		c.cancel()
		return 0, context.Canceled
	}
	n, err := c.src.Read(p)
	c.read += n
	return n, err
}
