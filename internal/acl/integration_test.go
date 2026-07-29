package acl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/storage"
)

// wired builds a storage layer guarded by a real ACL engine, over a real
// directory. These tests exercise the seam the whole design rests on: checks
// living inside storage rather than in the callers.
func wired(t *testing.T) (*harness, *storage.Local, string) {
	t.Helper()

	database, err := db.Open(t.Context(), db.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	h := &harness{engine: New(database)}
	h.alice = h.addUser(t, database, "alice", false)
	h.bob = h.addUser(t, database, "bob", false)
	h.admin = h.addUser(t, database, "admin", true)
	h.team = h.addGroup(t, database, "team")

	root := t.TempDir()
	fs, err := storage.Open(storage.Config{Root: root, Guard: h.engine, Reserve: 1})
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	return h, fs, root
}

// TestListHidesDeniedEntries covers the leak that filtering exists to prevent:
// a filename is often the whole secret, so a listing must not name what the
// caller may not open.
func TestListHidesDeniedEntries(t *testing.T) {
	t.Parallel()

	h, fs, root := wired(t)

	for _, name := range []string{"public.txt", "prive.txt", "aussi-prive.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	h.grant(t, SubjectUser, h.alice, "/", PermRead, true, false)
	h.grant(t, SubjectUser, h.alice, "/prive.txt", PermRead, false, true)
	h.grant(t, SubjectUser, h.alice, "/aussi-prive.txt", PermRead, false, true)

	ctx := NewContext(t.Context(), h.subject(h.alice, false))
	entries, err := fs.List(ctx, storage.Root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 1 || entries[0].Name != "public.txt" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		t.Fatalf("List returned %v, want only public.txt", names)
	}

	// What is hidden from the listing must also be unopenable — a listing that
	// merely omits entries while leaving them reachable is decoration.
	if _, err := fs.Open(ctx, storage.MustParsePath("/prive.txt")); !errors.Is(err, storage.ErrPermission) {
		t.Errorf("Open on a hidden entry = %v, want ErrPermission", err)
	}
}

// TestNoStorageCallBypassesTheGuard is the other half of this lot's completion
// criterion. Every method that touches data is called with a subject holding
// nothing, and every one must refuse.
//
// The table is deliberately exhaustive rather than representative: a method
// added later without a check is exactly the mistake this guards against, and
// it will show up as a missing entry here.
func TestNoStorageCallBypassesTheGuard(t *testing.T) {
	t.Parallel()

	h, fs, root := wired(t)

	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Alice exists but holds no permission anywhere.
	ctx := NewContext(t.Context(), h.subject(h.alice, false))
	file := storage.MustParsePath("/file.txt")
	dir := storage.MustParsePath("/dir")

	calls := map[string]func() error{
		"Stat": func() error {
			_, err := fs.Stat(ctx, file)
			return err
		},
		"List": func() error {
			_, err := fs.List(ctx, dir)
			return err
		},
		"Open": func() error {
			f, err := fs.Open(ctx, file)
			if err == nil {
				_ = f.Close()
			}
			return err
		},
		"Create": func() error {
			w, err := fs.Create(ctx, storage.MustParsePath("/nouveau.txt"))
			if err == nil {
				_ = w.Close()
			}
			return err
		},
		"Mkdir": func() error {
			return fs.Mkdir(ctx, storage.MustParsePath("/nouveau"))
		},
		"MkdirAll": func() error {
			return fs.MkdirAll(ctx, storage.MustParsePath("/a/b/c"))
		},
		"Move": func() error {
			return fs.Move(ctx, file, storage.MustParsePath("/ailleurs.txt"))
		},
		"Remove": func() error {
			return fs.Remove(ctx, file)
		},
		"RemoveAll": func() error {
			return fs.RemoveAll(ctx, dir)
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, storage.ErrPermission) {
				t.Fatalf("%s = %v, want ErrPermission", name, err)
			}
		})
	}

	// Nothing was actually performed.
	if _, err := os.Stat(filepath.Join(root, "file.txt")); err != nil {
		t.Errorf("the file was affected despite every call being refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nouveau")); !os.IsNotExist(err) {
		t.Error("a directory was created despite the refusal")
	}
}

// TestAnonymousReachesNothing checks the same surface without any subject at
// all, which is what an unauthenticated request looks like.
func TestAnonymousReachesNothing(t *testing.T) {
	t.Parallel()

	h, fs, root := wired(t)
	h.grant(t, SubjectUser, h.alice, "/", PermAll, true, false)

	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx := context.Background() // no subject
	if _, err := fs.Stat(ctx, storage.MustParsePath("/file.txt")); !errors.Is(err, storage.ErrPermission) {
		t.Errorf("Stat = %v, want ErrPermission", err)
	}
	if _, err := fs.List(ctx, storage.Root); !errors.Is(err, storage.ErrPermission) {
		t.Errorf("List = %v, want ErrPermission", err)
	}
}

// TestMoveNeedsDeleteOnItsSource covers a specific escalation: moving a file
// out of a directory removes it from that directory, so read on the source
// plus write on the destination must not be enough.
func TestMoveNeedsDeleteOnItsSource(t *testing.T) {
	t.Parallel()

	h, fs, root := wired(t)

	if err := os.MkdirAll(filepath.Join(root, "lecture"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "depot"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "lecture", "rapport.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	h.grant(t, SubjectUser, h.alice, "/lecture", PermRead, true, false)
	h.grant(t, SubjectUser, h.alice, "/depot", PermRead|PermWrite|PermDelete, true, false)

	ctx := NewContext(t.Context(), h.subject(h.alice, false))
	from := storage.MustParsePath("/lecture/rapport.pdf")
	to := storage.MustParsePath("/depot/rapport.pdf")

	if err := fs.Move(ctx, from, to); !errors.Is(err, storage.ErrPermission) {
		t.Fatalf("Move = %v, want ErrPermission — read on the source was enough to empty it", err)
	}
	if _, err := os.Stat(filepath.Join(root, "lecture", "rapport.pdf")); err != nil {
		t.Errorf("the file left its directory: %v", err)
	}

	// With delete on the source it goes through.
	h.grant(t, SubjectUser, h.alice, "/lecture", PermDelete, true, false)
	if err := fs.Move(ctx, from, to); err != nil {
		t.Fatalf("Move with delete on the source: %v", err)
	}
}

// TestOwnerCanActOnWhatTheyUploaded closes the loop between ownership and the
// storage layer: no explicit rule, only the ownership record.
func TestOwnerCanActOnWhatTheyUploaded(t *testing.T) {
	t.Parallel()

	h, fs, root := wired(t)

	if err := os.MkdirAll(filepath.Join(root, "uploads"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "uploads", "photo.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	p := storage.MustParsePath("/uploads/photo.jpg")
	if err := h.engine.SetOwner(t.Context(), p, h.alice); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}

	alice := NewContext(t.Context(), h.subject(h.alice, false))
	if _, err := fs.Stat(alice, p); err != nil {
		t.Errorf("the uploader cannot stat their own file: %v", err)
	}

	// Ownership is personal: Bob uploaded nothing here.
	bob := NewContext(t.Context(), h.subject(h.bob, false))
	if _, err := fs.Stat(bob, p); !errors.Is(err, storage.ErrPermission) {
		t.Errorf("another account reached the file through ownership: %v", err)
	}

	if err := fs.Remove(alice, p); err != nil {
		t.Errorf("the uploader cannot delete their own file: %v", err)
	}
}

// TestAdminReachesEverythingThroughStorage checks the bypass end to end, not
// just in the engine.
func TestAdminReachesEverythingThroughStorage(t *testing.T) {
	t.Parallel()

	h, fs, root := wired(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx := NewContext(t.Context(), h.subject(h.admin, true))
	if _, err := fs.Stat(ctx, storage.MustParsePath("/file.txt")); err != nil {
		t.Errorf("an administrator was refused: %v", err)
	}
	entries, err := fs.List(ctx, storage.Root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("an administrator saw %d entries, want 1", len(entries))
	}
}
