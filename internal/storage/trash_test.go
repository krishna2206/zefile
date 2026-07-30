package storage

import (
	"context"
	"errors"
	"testing"
)

func putFile(t *testing.T, fs *Local, p Path, content string) {
	t.Helper()
	w, err := fs.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("Create %s: %v", p, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("Write %s: %v", p, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close %s: %v", p, err)
	}
}

func TestTrashRoundTrip(t *testing.T) {
	root := t.TempDir()
	fs := mustOpen(t, Config{Root: root, Guard: &recordingGuard{}, Reserve: 1})
	ctx := context.Background()

	file := MustParsePath("/notes.txt")
	putFile(t, fs, file, "hello")

	id, err := fs.Trash(ctx, file)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// Gone from its place.
	if _, err := fs.Stat(ctx, file); !errors.Is(err, ErrNotExist) {
		t.Fatalf("stat after trash = %v, want ErrNotExist", err)
	}
	// And absent from the listing: the trash directory is reserved, so neither
	// the entry nor the directory holding it ever shows.
	entries, err := fs.List(ctx, MustParsePath("/"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.Name == "notes.txt" || e.Name == TrashDir {
			t.Fatalf("trash leaked into listing: %q", e.Name)
		}
	}

	// Restore brings it back to exactly where it was.
	if err := fs.Restore(ctx, id, file); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := fs.Stat(ctx, file); err != nil {
		t.Fatalf("stat after restore: %v", err)
	}
}

func TestRestoreRefusesOccupiedDestination(t *testing.T) {
	root := t.TempDir()
	fs := mustOpen(t, Config{Root: root, Guard: &recordingGuard{}, Reserve: 1})
	ctx := context.Background()

	file := MustParsePath("/report.pdf")
	putFile(t, fs, file, "one")

	id, err := fs.Trash(ctx, file)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// A new file takes the vacated name before the restore is attempted.
	putFile(t, fs, file, "two")

	if err := fs.Restore(ctx, id, file); !errors.Is(err, ErrExist) {
		t.Fatalf("Restore over existing = %v, want ErrExist", err)
	}
}

func TestPurgeTrashIsIdempotent(t *testing.T) {
	root := t.TempDir()
	fs := mustOpen(t, Config{Root: root, Guard: &recordingGuard{}, Reserve: 1})
	ctx := context.Background()

	file := MustParsePath("/tmp.bin")
	putFile(t, fs, file, "x")
	id, err := fs.Trash(ctx, file)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	if err := fs.PurgeTrash(ctx, id); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	// Purging one already gone is not an error, so emptying never half-fails.
	if err := fs.PurgeTrash(ctx, id); err != nil {
		t.Fatalf("Purge again: %v", err)
	}
}
