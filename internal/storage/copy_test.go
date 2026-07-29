package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	payload := bytes.Repeat([]byte("disc image "), 1000)
	if err := os.WriteFile(filepath.Join(root, "jeu.iso"), payload, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()

	if err := fs.Copy(ctx, MustParsePath("/jeu.iso"), MustParsePath("/copie.iso")); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "copie.iso"))
	if err != nil {
		t.Fatalf("read the copy: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("the copy differs from the source (%d vs %d bytes)", len(got), len(payload))
	}

	// The original is untouched.
	if original, err := os.ReadFile(filepath.Join(root, "jeu.iso")); err != nil || !bytes.Equal(original, payload) {
		t.Errorf("the source was altered: err=%v", err)
	}

	// No temporary file survived.
	if entries, err := os.ReadDir(filepath.Join(root, UploadsDir)); err == nil {
		if len(entries) != 0 {
			t.Errorf("%d temporary files left behind", len(entries))
		}
	}
}

func TestCopyRefusals(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dossier"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("y"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()

	cases := []struct {
		name     string
		from, to string
		want     error
	}{
		{"a directory needs the job queue", "/dossier", "/copie", ErrIsDir},
		{"a missing source", "/absent.txt", "/copie.txt", ErrNotExist},
		{"an existing destination is never overwritten", "/a.txt", "/b.txt", ErrExist},
		{"the root cannot be copied", "/", "/copie", ErrInvalidPath},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fs.Copy(ctx, MustParsePath(tc.from), MustParsePath(tc.to))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Copy = %v, want %v", err, tc.want)
			}
		})
	}

	// The refused overwrite left the destination alone.
	if content, err := os.ReadFile(filepath.Join(root, "b.txt")); err != nil || string(content) != "y" {
		t.Errorf("the destination was modified by a refused copy: %q err=%v", content, err)
	}
}

// TestCopySymlinkIsRefused: duplicating a link has no unsurprising meaning —
// copying the link, or copying what it points at, are both defensible and
// therefore both wrong to pick silently.
func TestCopySymlinkIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reel.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink("reel.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	err := fs.Copy(context.Background(), MustParsePath("/alias.txt"), MustParsePath("/copie.txt"))
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Copy of a symlink = %v, want ErrInvalidPath", err)
	}
}

func TestCopyRespectsTheSizeLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	big := filepath.Join(root, "gros.iso")

	// A sparse file: the size is what the limit reads, and no disk is spent.
	f, err := os.Create(big)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.Truncate(MaxSyncCopyBytes + 1); err != nil {
		_ = f.Close()
		t.Skipf("sparse files unavailable: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	err = fs.Copy(context.Background(), MustParsePath("/gros.iso"), MustParsePath("/copie.iso"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Copy = %v, want ErrTooLarge", err)
	}
	if _, err := os.Stat(filepath.Join(root, "copie.iso")); !os.IsNotExist(err) {
		t.Error("a destination was created despite the refusal")
	}
}

// TestCopyLeavesNoPartialFile is the reason for the temporary-file dance: a
// cancelled copy must not leave something at the destination that looks like a
// complete file.
func TestCopyLeavesNoPartialFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	payload := bytes.Repeat([]byte("z"), 4<<20)
	if err := os.WriteFile(filepath.Join(root, "source.bin"), payload, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before a single byte moves

	err := fs.Copy(ctx, MustParsePath("/source.bin"), MustParsePath("/copie.bin"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Copy = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(root, "copie.bin")); !os.IsNotExist(err) {
		t.Fatal("a cancelled copy left a file at the destination")
	}
	if entries, err := os.ReadDir(filepath.Join(root, UploadsDir)); err == nil && len(entries) != 0 {
		t.Errorf("%d temporary files left behind after cancellation", len(entries))
	}
}

func TestCopyRefusesWhenOutOfSpace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1 << 62})
	err := fs.Copy(context.Background(), MustParsePath("/a.txt"), MustParsePath("/b.txt"))
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Copy = %v, want ErrNoSpace", err)
	}
}

// TestCopyTemporaryFilesStayHidden checks that an in-flight copy is never
// visible in a listing.
func TestCopyTemporaryFilesStayHidden(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()
	if err := fs.Copy(ctx, MustParsePath("/a.txt"), MustParsePath("/b.txt")); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	entries, err := fs.List(ctx, Root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if isReservedName(e.Name) {
			t.Errorf("the reserved directory %q appeared in a listing", e.Name)
		}
	}
	if len(entries) != 2 {
		t.Errorf("listing has %d entries, want the two files", len(entries))
	}
}

func TestCopyChecksPermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	guard := &recordingGuard{}
	fs := mustOpen(t, Config{Root: root, Guard: guard, Reserve: 1})
	if err := fs.Copy(context.Background(), MustParsePath("/a.txt"), MustParsePath("/b.txt")); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if !guard.sawOp(OpRead) {
		t.Errorf("Copy did not require read on its source; checks were: %s", guard)
	}
	if !guard.sawOp(OpWrite) {
		t.Errorf("Copy did not require write on its destination; checks were: %s", guard)
	}
	// Copying leaves the source in place, so delete must not be demanded.
	if guard.sawOp(OpDelete) {
		t.Errorf("Copy demanded delete on its source; checks were: %s", guard)
	}
}
