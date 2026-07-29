package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file exist to answer one question: can anything reach a
// byte outside the storage root? Every one of them must keep failing, so a
// change that makes one pass is a regression even if everything else is green.

// secretOutside builds a directory next to the storage root holding a file no
// caller must ever reach, and returns the root and the secret's host path.
func secretOutside(t *testing.T) (root, secret string) {
	t.Helper()

	base := t.TempDir()
	root = filepath.Join(base, "storage")
	outside := filepath.Join(base, "outside")

	for _, dir := range []string{root, outside} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	secret = filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("this must never be served"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return root, secret
}

func TestTraversalIsRejectedAtParse(t *testing.T) {
	t.Parallel()

	// Escapes are stopped before a syscall is ever attempted. This is defence
	// in depth, not the primary control: os.Root would refuse them anyway.
	for _, attempt := range []string{
		"/../secret.txt",
		"/../outside/secret.txt",
		"/a/../../outside/secret.txt",
		"/./../outside/secret.txt",
		"/a/b/../../../outside/secret.txt",
	} {
		if _, err := ParsePath(attempt); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ParsePath(%q) error = %v, want ErrInvalidPath", attempt, err)
		}
	}
}

func TestSymlinkCannotEscapeRoot(t *testing.T) {
	t.Parallel()

	root, secret := secretOutside(t)

	// A symlink planted inside the root — by an administrator over SSH, or by
	// an archive extracted before the extraction guards existed.
	link := filepath.Join(root, "leak")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs := mustOpen(t, Config{Root: root})
	ctx := context.Background()
	p := MustParsePath("/leak")

	if f, err := fs.Open(ctx, p); err == nil {
		f.Close()
		t.Fatal("Open followed a symlink out of the root")
	}

	// The link is still visible, and reported as a link. Hiding it would be
	// worse: an administrator cannot clean up what the interface denies exists.
	info, err := fs.Stat(ctx, p)
	if err != nil {
		t.Fatalf("Stat on the link itself failed: %v", err)
	}
	if !info.Symlink {
		t.Error("Stat did not report the entry as a symlink")
	}
}

func TestSymlinkedDirectoryCannotEscapeRoot(t *testing.T) {
	t.Parallel()

	root, secret := secretOutside(t)
	outside := filepath.Dir(secret)

	if err := os.Symlink(outside, filepath.Join(root, "door")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs := mustOpen(t, Config{Root: root})
	ctx := context.Background()

	if f, err := fs.Open(ctx, MustParsePath("/door/secret.txt")); err == nil {
		f.Close()
		t.Fatal("Open reached a file through a directory symlink leaving the root")
	}
	if _, err := fs.List(ctx, MustParsePath("/door")); err == nil {
		t.Fatal("List enumerated a directory outside the root")
	}
}

func TestAbsoluteSymlinkIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Symlink("/etc", filepath.Join(root, "etc")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs := mustOpen(t, Config{Root: root})
	if _, err := fs.List(context.Background(), MustParsePath("/etc")); err == nil {
		t.Fatal("List followed an absolute symlink")
	}
}

// TestSymlinkWithinRootStillWorks is the counterweight to the tests above:
// confinement must not be achieved by refusing every link.
func TestSymlinkWithinRootStillWorks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	if err := os.WriteFile(target, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fs := mustOpen(t, Config{Root: root})
	f, err := fs.Open(context.Background(), MustParsePath("/alias.txt"))
	if err != nil {
		t.Fatalf("Open through an internal symlink failed: %v", err)
	}
	defer f.Close()

	got := make([]byte, 7)
	if _, err := f.Read(got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("read %q, want %q", got, "payload")
	}
}

func TestReservedDirectoriesAreUnreachable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{TrashDir, UploadsDir} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "inside"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write in %s: %v", name, err)
		}
	}

	fs := mustOpen(t, Config{Root: root})
	ctx := context.Background()

	for _, target := range []string{
		"/" + TrashDir,
		"/" + TrashDir + "/inside",
		"/" + UploadsDir,
		"/" + UploadsDir + "/inside",
	} {
		p := MustParsePath(target)
		if _, err := fs.Stat(ctx, p); !errors.Is(err, ErrReserved) {
			t.Errorf("Stat(%q) error = %v, want ErrReserved", target, err)
		}
		if _, err := fs.Open(ctx, p); !errors.Is(err, ErrReserved) {
			t.Errorf("Open(%q) error = %v, want ErrReserved", target, err)
		}
		if err := fs.RemoveAll(ctx, p); !errors.Is(err, ErrReserved) {
			t.Errorf("RemoveAll(%q) error = %v, want ErrReserved", target, err)
		}
	}

	entries, err := fs.List(ctx, Root)
	if err != nil {
		t.Fatalf("List root: %v", err)
	}
	for _, e := range entries {
		if isReservedName(e.Name) {
			t.Errorf("List exposed the reserved directory %q", e.Name)
		}
	}
}

// TestGuardRefusalIsOpaque checks that a refusal never reveals whether the
// target exists. Distinguishable errors turn the permission system into an
// oracle for enumerating paths.
func TestGuardRefusalIsOpaque(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Guard: denyAll{}})
	ctx := context.Background()

	_, existing := fs.Stat(ctx, MustParsePath("/exists.txt"))
	_, missing := fs.Stat(ctx, MustParsePath("/absent.txt"))

	if !errors.Is(existing, ErrPermission) || !errors.Is(missing, ErrPermission) {
		t.Fatalf("want ErrPermission for both, got %v and %v", existing, missing)
	}
	if existing.Error() != missing.Error() {
		t.Fatalf("refusals are distinguishable: %q vs %q", existing, missing)
	}
}

// TestGuardFailureDoesNotFallOpen checks that a Guard which cannot decide
// refuses. An ACL engine that loses its database must lock the doors, not open
// them.
func TestGuardFailureDoesNotFallOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Guard: brokenGuard{}})
	if _, err := fs.Stat(context.Background(), MustParsePath("/f.txt")); err == nil {
		t.Fatal("a Guard that failed to decide allowed the operation")
	}
}

func TestMoveCannotEscapeOrRecurse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()

	// Moving a directory inside itself would detach the subtree from the tree.
	err := fs.Move(ctx, MustParsePath("/dir"), MustParsePath("/dir/sub/dir"))
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("move into itself: error = %v, want ErrInvalidPath", err)
	}

	// A sibling whose name merely starts with the same letters is not inside it.
	if err := os.Mkdir(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := fs.Move(ctx, MustParsePath("/other"), MustParsePath("/dirtoo")); err != nil {
		t.Errorf("move to a similarly named sibling failed: %v", err)
	}

	// The root itself is not movable.
	if err := fs.Move(ctx, Root, MustParsePath("/anywhere")); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("move root: error = %v, want ErrInvalidPath", err)
	}
}

func TestContainsPathDetectsNesting(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "data")
	nested := filepath.Join(root, "config")
	sibling := filepath.Join(base, "config")

	for _, dir := range []string{root, nested, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	cases := []struct {
		name  string
		other string
		want  bool
	}{
		{"nested", nested, true},
		{"sibling", sibling, false},
		{"root itself", root, true},
		{"not yet created but nested", filepath.Join(root, "future"), true},
		// A name sharing a prefix is not nested: "/data-backup" is not in "/data".
		{"prefix lookalike", root + "-backup", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ContainsPath(root, tc.other)
			if err != nil {
				t.Fatalf("ContainsPath: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ContainsPath(%q, %q) = %v, want %v", root, tc.other, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------- helpers

func mustOpen(t *testing.T, cfg Config) *Local {
	t.Helper()
	fs, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

type denyAll struct{}

func (denyAll) Authorize(context.Context, Op, Path) error { return ErrPermission }

type brokenGuard struct{}

func (brokenGuard) Authorize(context.Context, Op, Path) error {
	return errors.New("acl database unreachable")
}

// recordingGuard captures what the storage layer asked for, so tests can assert
// that the right operation was checked rather than merely that some check ran.
type recordingGuard struct {
	ops   []Op
	paths []string
}

func (g *recordingGuard) Authorize(_ context.Context, op Op, p Path) error {
	g.ops = append(g.ops, op)
	g.paths = append(g.paths, p.String())
	return nil
}

func (g *recordingGuard) sawOp(op Op) bool {
	for _, got := range g.ops {
		if got == op {
			return true
		}
	}
	return false
}

func (g *recordingGuard) String() string {
	parts := make([]string, len(g.ops))
	for i := range g.ops {
		parts[i] = g.ops[i].String() + " " + g.paths[i]
	}
	return strings.Join(parts, ", ")
}
