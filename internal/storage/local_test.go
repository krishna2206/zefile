package storage

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsBadRoots(t *testing.T) {
	t.Parallel()

	t.Run("missing root", func(t *testing.T) {
		t.Parallel()
		if _, err := Open(Config{Root: filepath.Join(t.TempDir(), "absent")}); err == nil {
			t.Fatal("Open accepted a root that does not exist")
		}
	})

	t.Run("root is a file", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "f")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := Open(Config{Root: file}); err == nil {
			t.Fatal("Open accepted a file as the root")
		}
	})

	t.Run("empty root", func(t *testing.T) {
		t.Parallel()
		if _, err := Open(Config{}); err == nil {
			t.Fatal("Open accepted an empty root")
		}
	})
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	fs := mustOpen(t, Config{Root: t.TempDir(), Reserve: 1})
	ctx := context.Background()

	dir := MustParsePath("/jeux/steam")
	if err := fs.MkdirAll(ctx, dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	file := MustParsePath("/jeux/steam/jeu.iso")
	w, err := fs.Create(ctx, file)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload := []byte("disc image contents")
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	info, err := fs.Stat(ctx, file)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(payload)) {
		t.Errorf("Size = %d, want %d", info.Size, len(payload))
	}
	if info.IsDir {
		t.Error("IsDir = true for a file")
	}
	if info.Name != "jeu.iso" {
		t.Errorf("Name = %q, want %q", info.Name, "jeu.iso")
	}

	r, err := fs.Open(ctx, file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("read %q, want %q", got, payload)
	}

	// Seek support is what makes HTTP range requests possible, so it is part of
	// the contract rather than an implementation detail.
	if _, err := r.Seek(5, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	tail, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll after seek: %v", err)
	}
	if string(tail) != string(payload[5:]) {
		t.Fatalf("after seek read %q, want %q", tail, payload[5:])
	}

	entries, err := fs.List(ctx, dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != file {
		t.Fatalf("List = %+v, want one entry at %q", entries, file)
	}

	moved := MustParsePath("/jeux/steam/renommé.iso")
	if err := fs.Move(ctx, file, moved); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := fs.Stat(ctx, file); !errors.Is(err, ErrNotExist) {
		t.Errorf("after Move, old path error = %v, want ErrNotExist", err)
	}
	if _, err := fs.Stat(ctx, moved); err != nil {
		t.Errorf("after Move, new path: %v", err)
	}

	if err := fs.Remove(ctx, moved); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := fs.Stat(ctx, moved); !errors.Is(err, ErrNotExist) {
		t.Errorf("after Remove, error = %v, want ErrNotExist", err)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir", "child"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"stat missing", func() error {
			_, err := fs.Stat(ctx, MustParsePath("/nope"))
			return err
		}, ErrNotExist},
		{"open a directory", func() error {
			_, err := fs.Open(ctx, MustParsePath("/dir"))
			return err
		}, ErrIsDir},
		{"list a file", func() error {
			_, err := fs.List(ctx, MustParsePath("/file.txt"))
			return err
		}, ErrNotDir},
		{"mkdir over an existing entry", func() error {
			return fs.Mkdir(ctx, MustParsePath("/dir"))
		}, ErrExist},
		{"remove a non-empty directory", func() error {
			return fs.Remove(ctx, MustParsePath("/dir"))
		}, ErrNotEmpty},
		// Treating an intermediate file as a directory reports exactly that,
		// rather than a vague "not found" that would send the caller looking
		// for the wrong problem.
		{"descend through a file", func() error {
			_, err := fs.Stat(ctx, MustParsePath("/file.txt/deeper"))
			return err
		}, ErrNotDir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	// RemoveAll is idempotent, matching os.RemoveAll, so a retried delete after
	// a dropped connection is not an error.
	if err := fs.RemoveAll(ctx, MustParsePath("/never-existed")); err != nil {
		t.Errorf("RemoveAll on a missing path = %v, want nil", err)
	}
}

func TestReadOnlyRefusesWritesButServesReads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, ReadOnly: true})
	ctx := context.Background()

	if _, err := fs.Create(ctx, MustParsePath("/new.txt")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Create = %v, want ErrReadOnly", err)
	}
	if err := fs.Mkdir(ctx, MustParsePath("/new")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Mkdir = %v, want ErrReadOnly", err)
	}
	if err := fs.Remove(ctx, MustParsePath("/existing.txt")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Remove = %v, want ErrReadOnly", err)
	}

	if _, err := fs.Stat(ctx, MustParsePath("/existing.txt")); err != nil {
		t.Errorf("Stat in read-only mode: %v", err)
	}
	if _, err := fs.List(ctx, Root); err != nil {
		t.Errorf("List in read-only mode: %v", err)
	}
}

// TestFullDiskStillAllowsDeletion is the behaviour that keeps an instance
// recoverable: when free space falls below the reserve, writes stop but reads
// and deletions continue, so an administrator can still sign in and reclaim
// space instead of finding a service that will not start.
func TestFullDiskStillAllowsDeletion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.iso"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A reserve no real volume can satisfy simulates a full disk.
	fs := mustOpen(t, Config{Root: root, Reserve: math.MaxUint64})
	ctx := context.Background()

	if _, err := fs.Create(ctx, MustParsePath("/more.iso")); !errors.Is(err, ErrNoSpace) {
		t.Errorf("Create = %v, want ErrNoSpace", err)
	}
	if err := fs.Mkdir(ctx, MustParsePath("/dir")); !errors.Is(err, ErrNoSpace) {
		t.Errorf("Mkdir = %v, want ErrNoSpace", err)
	}

	if _, err := fs.Open(ctx, MustParsePath("/big.iso")); err != nil {
		t.Errorf("Open with a full disk: %v", err)
	}
	if err := fs.Remove(ctx, MustParsePath("/big.iso")); err != nil {
		t.Errorf("Remove with a full disk: %v — recovery would be impossible", err)
	}

	info, err := fs.Space(ctx)
	if err != nil {
		t.Fatalf("Space: %v", err)
	}
	if !info.ReadOnly {
		t.Error("Space did not report read-only while below the reserve")
	}
}

func TestSpaceReportsVolume(t *testing.T) {
	t.Parallel()

	fs := mustOpen(t, Config{Root: t.TempDir()})
	info, err := fs.Space(context.Background())
	if err != nil {
		t.Skipf("free space unavailable on this platform: %v", err)
	}
	if info.Total == 0 {
		t.Error("Total = 0")
	}
	if info.Available > info.Total {
		t.Errorf("Available %d exceeds Total %d", info.Available, info.Total)
	}
	if info.Reserve != DefaultReserve {
		t.Errorf("Reserve = %d, want the default %d", info.Reserve, DefaultReserve)
	}
}

// TestGuardSeesTheRightOperation checks that authorisation is asked the correct
// question. A move that only needed read on its source would let someone empty
// a directory they may merely look at.
func TestGuardSeesTheRightOperation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	guard := &recordingGuard{}
	fs := mustOpen(t, Config{Root: root, Guard: guard, Reserve: 1})
	ctx := context.Background()

	if err := fs.Move(ctx, MustParsePath("/a.txt"), MustParsePath("/b.txt")); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if !guard.sawOp(OpDelete) {
		t.Errorf("Move did not require delete on its source; checks were: %s", guard)
	}
	if !guard.sawOp(OpWrite) {
		t.Errorf("Move did not require write on its destination; checks were: %s", guard)
	}

	guard.ops, guard.paths = nil, nil
	if _, err := fs.Open(ctx, MustParsePath("/b.txt")); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !guard.sawOp(OpRead) {
		t.Errorf("Open did not require read; checks were: %s", guard)
	}
}
