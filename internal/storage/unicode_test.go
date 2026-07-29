package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// Two spellings of the same French word. Written as code points because the
// literals are indistinguishable on screen, which is precisely why this class
// of bug survives code review.
const (
	nfcName = "r\u00e9sum\u00e9.txt"   // e-acute as a single rune
	nfdName = "re\u0301sume\u0301.txt" // e plus a combining accent
	nfcDir  = "dossier-\u00e9t\u00e9"
	nfdDir  = "dossier-e\u0301te\u0301"
)

// TestFindsFileWrittenInDecomposedForm covers the case that actually happens:
// a file copied onto the server over SSH from a Mac, whose name is stored
// decomposed. It must be findable by the composed path the API speaks.
func TestFindsFileWrittenInDecomposedForm(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, nfdName), []byte("contenu"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()
	p := MustParsePath("/" + nfcName)

	info, err := fs.Stat(ctx, p)
	if err != nil {
		t.Fatalf("Stat with the composed path failed: %v", err)
	}
	if info.Size != int64(len("contenu")) {
		t.Errorf("Size = %d, want %d", info.Size, len("contenu"))
	}

	f, err := fs.Open(ctx, p)
	if err != nil {
		t.Fatalf("Open with the composed path failed: %v", err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "contenu" {
		t.Fatalf("read %q, want %q", got, "contenu")
	}
}

// TestFindsFileThroughDecomposedDirectory exercises the resolver on an
// intermediate component, not just the final name.
func TestFindsFileThroughDecomposedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, nfdDir)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, nfdName), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()

	if _, err := fs.Stat(ctx, MustParsePath("/"+nfcDir+"/"+nfcName)); err != nil {
		t.Fatalf("Stat through a decomposed directory failed: %v", err)
	}

	// Creating inside a decomposed directory must land in that directory
	// rather than making a second one in composed form.
	if err := fs.Mkdir(ctx, MustParsePath("/"+nfcDir+"/sous")); err != nil {
		t.Fatalf("Mkdir inside a decomposed directory: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("root holds %d entries %q, want the single existing directory", len(entries), names)
	}
}

// TestListReportsComposedNames checks that what leaves the storage layer is
// always key form, whatever the disk holds. Anything else and an ACL entry
// recorded from a listing would not match the same file later.
func TestListReportsComposedNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, nfdName), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fs := mustOpen(t, Config{Root: root})
	entries, err := fs.List(context.Background(), Root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].Name != nfcName {
		t.Errorf("Name = %q, want the composed form %q", entries[0].Name, nfcName)
	}
	if want := "/" + nfcName; entries[0].Path.String() != want {
		t.Errorf("Path = %q, want %q", entries[0].Path, want)
	}
}

// TestNewFilesAreWrittenComposed keeps the tree uniformly normalised, so the
// fallback resolver stays a rare path rather than the common one.
func TestNewFilesAreWrittenComposed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fs := mustOpen(t, Config{Root: root, Reserve: 1})

	// The client sends the decomposed spelling; disk must receive composed.
	p, err := ParsePath("/" + nfdName)
	if err != nil {
		t.Fatalf("ParsePath: %v", err)
	}
	w, err := fs.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	// macOS filesystems may normalise names themselves, so an exact byte match
	// is not portable. What must hold everywhere is that the file is reachable
	// through the composed path.
	if _, err := fs.Stat(context.Background(), MustParsePath("/"+nfcName)); err != nil {
		t.Errorf("file created from a decomposed request is not reachable composed: %v", err)
	}
}

// TestAmbiguousNamesAreRefused covers a directory holding both spellings of one
// name. Picking either would mean acting on a file the caller did not name, so
// the operation must fail instead.
func TestAmbiguousNamesAreRefused(t *testing.T) {
	t.Parallel()

	// Two combining marks of different canonical classes, written in both
	// orders. Canonical ordering makes them equal, and — unlike a plain
	// composed/decomposed pair — neither spelling is itself the NFC form.
	//
	// That matters: if one of them were the NFC form, the fast path would find
	// it byte-for-byte and the ambiguity branch would never be reached, so the
	// test would pass while proving nothing.
	const (
		cedillaThenAcute = "c\u0327\u0301.txt" // cedilla (class 202) then acute (class 230)
		acuteThenCedilla = "c\u0301\u0327.txt" // the same marks, written the other way round
	)
	canonical := norm.NFC.String(cedillaThenAcute)

	if canonical == cedillaThenAcute || canonical == acuteThenCedilla {
		t.Fatal("a spelling coincides with its NFC form; this test would take the fast path")
	}
	if canonical != norm.NFC.String(acuteThenCedilla) {
		t.Fatal("the two spellings do not share an NFC form; nothing would be ambiguous")
	}

	root := t.TempDir()
	for _, name := range []string{cedillaThenAcute, acuteThenCedilla} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}

	// A filesystem that normalises names, as APFS does, cannot hold both at
	// once — the second write simply replaced the first.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) < 2 {
		t.Skip("filesystem normalises names; ambiguity cannot arise here")
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	ctx := context.Background()
	p := MustParsePath("/" + canonical)

	if _, err := fs.Stat(ctx, p); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("Stat = %v, want ErrAmbiguous", err)
	}
	// Destructive operations especially must refuse rather than guess which of
	// the two files was meant.
	if err := fs.RemoveAll(ctx, p); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("RemoveAll = %v, want ErrAmbiguous", err)
	}
}
