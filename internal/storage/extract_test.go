package storage

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeZip builds a ZIP from the given entries and writes it into the storage
// root under name, returning nothing — the archive is then extractable by path.
// A nil body means a directory entry.
func writeZip(t *testing.T, root, name string, entries []zipEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.body == nil {
			hdr.Name = e.name + "/"
			hdr.SetMode(os.ModeDir | 0o755)
			if _, err := zw.CreateHeader(hdr); err != nil {
				t.Fatalf("zip dir %q: %v", e.name, err)
			}
			continue
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip entry %q: %v", e.name, err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatalf("zip write %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

type zipEntry struct {
	name string
	body []byte // nil means a directory
}

func TestExtractZip(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeZip(t, root, "bundle.zip", []zipEntry{
		{name: "readme.txt", body: []byte("hello")},
		{name: "docs", body: nil},
		{name: "docs/guide.md", body: []byte("# guide")},
	})

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	target, err := fs.ExtractZip(context.Background(), MustParsePath("/bundle.zip"), MustParsePath("/"), nil)
	if err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}
	if target.String() != "/bundle" {
		t.Fatalf("target = %q, want /bundle", target.String())
	}

	got, err := os.ReadFile(filepath.Join(root, "bundle", "docs", "guide.md"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(got) != "# guide" {
		t.Fatalf("extracted content = %q", got)
	}

	// No temporary tree survived.
	if entries, err := os.ReadDir(filepath.Join(root, UploadsDir)); err == nil && len(entries) != 0 {
		t.Errorf("%d temporary entries left behind", len(entries))
	}
}

func TestExtractRefusesExistingDestination(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeZip(t, root, "bundle.zip", []zipEntry{{name: "a.txt", body: []byte("x")}})
	if err := os.Mkdir(filepath.Join(root, "bundle"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	_, err := fs.ExtractZip(context.Background(), MustParsePath("/bundle.zip"), MustParsePath("/"), nil)
	if !errors.Is(err, ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
}

// TestExtractRefusesTraversal is half of the roadmap's "done when": an archive
// whose entry name climbs out of the destination must be refused, and nothing
// must be written outside the tree.
func TestExtractRefusesTraversal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeZip(t, root, "evil.zip", []zipEntry{
		{name: "../escape.txt", body: []byte("pwned")},
	})

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	_, err := fs.ExtractZip(context.Background(), MustParsePath("/evil.zip"), MustParsePath("/"), nil)
	if !errors.Is(err, ErrBadArchive) {
		t.Fatalf("err = %v, want ErrBadArchive", err)
	}
	// The sibling of the root must not have been created.
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); err == nil {
		t.Fatal("traversal wrote a file outside the root")
	}
	// And the partial tree was cleaned up.
	if entries, err := os.ReadDir(filepath.Join(root, UploadsDir)); err == nil && len(entries) != 0 {
		t.Errorf("%d temporary entries left behind", len(entries))
	}
}

// TestExtractRefusesBomb is the other half: an entry that expands far past its
// compressed size must be refused, and refused on bytes actually written rather
// than the header's claim.
func TestExtractRefusesBomb(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Four megabytes of zeros deflate to a few kilobytes — a ratio in the
	// thousands, well past MaxCompressionRatio, and larger than the ratio floor.
	writeZip(t, root, "bomb.zip", []zipEntry{
		{name: "big.bin", body: make([]byte, 4<<20)},
	})

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	_, err := fs.ExtractZip(context.Background(), MustParsePath("/bomb.zip"), MustParsePath("/"), nil)
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("err = %v, want ErrArchiveTooLarge", err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, UploadsDir)); err == nil && len(entries) != 0 {
		t.Errorf("%d temporary entries left behind", len(entries))
	}
}

// TestExtractIgnoresSymlinks confirms a symlink entry is skipped rather than
// reproduced: an archived symlink is a classic way to point at a file outside
// the tree after the fact.
func TestExtractIgnoresSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "link"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("create symlink entry: %v", err)
	}
	if _, err := w.Write([]byte("/etc/passwd")); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	// A regular file alongside it, so the archive is not empty.
	rw, _ := zw.Create("ok.txt")
	_, _ = rw.Write([]byte("fine"))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "links.zip"), buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	fs := mustOpen(t, Config{Root: root, Reserve: 1})
	target, err := fs.ExtractZip(context.Background(), MustParsePath("/links.zip"), MustParsePath("/"), nil)
	if err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, target.rel(), "link")); err == nil {
		t.Fatal("a symlink entry was reproduced")
	}
	if _, err := os.Stat(filepath.Join(root, target.rel(), "ok.txt")); err != nil {
		t.Fatalf("the regular entry was not extracted: %v", err)
	}
}
