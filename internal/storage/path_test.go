package storage

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePathRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"relative", "etc/passwd"},
		{"bare traversal", ".."},
		{"leading traversal", "/../etc/passwd"},
		{"embedded traversal", "/a/../../etc/passwd"},
		{"trailing traversal", "/a/b/.."},
		{"current directory", "/./a"},
		{"current directory alone", "/."},
		{"double separator", "/a//b"},
		{"NUL byte", "/a\x00b"},
		{"newline", "/a\nb"},
		{"carriage return", "/a\rb"},
		{"tab", "/a\tb"},
		{"delete character", "/a\x7fb"},
		{"invalid UTF-8", "/a\xff\xfeb"},
		{"component too long", "/" + strings.Repeat("x", MaxComponentBytes+1)},
		{"path too long", "/" + strings.Repeat("a/", MaxPathBytes)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, err := ParsePath(tc.in); err == nil {
				t.Fatalf("ParsePath(%q) = %q, want error", tc.in, got)
			} else if !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("ParsePath(%q) error = %v, want ErrInvalidPath", tc.in, err)
			}
		})
	}
}

func TestParsePathAccepts(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"/", "/"},
		{"//", "/"},
		{"/a", "/a"},
		{"/a/b/c", "/a/b/c"},
		{"/a/", "/a"},
		{"/dossier avec espaces/fichier.iso", "/dossier avec espaces/fichier.iso"},
		{"/emoji-🎮/jeu.iso", "/emoji-🎮/jeu.iso"},

		// A backslash is an ordinary character in a POSIX filename. Confinement
		// is os.Root's job, which knows the host separator; rejecting the byte
		// here would make legitimate files unreachable for no security gain.
		{`/a\b`, `/a\b`},

		// Percent sequences are not decoded here. Decoding belongs to the HTTP
		// layer, and doing it twice is how traversal filters get bypassed.
		{"/a%2f..%2fb", "/a%2f..%2fb"},

		// A name that merely contains dots is not a traversal.
		{"/...", "/..."},
		{"/a..b", "/a..b"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePath(tc.in)
			if err != nil {
				t.Fatalf("ParsePath(%q) failed: %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Fatalf("ParsePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParsePathNormalises(t *testing.T) {
	t.Parallel()

	// Written as code points rather than literals: the two spellings are
	// visually identical in a source file, which is exactly what makes this
	// class of bug so easy to miss.
	const (
		composed   = "/caf\u00e9/r\u00e9sum\u00e9.txt"    // e-acute as one rune
		decomposed = "/cafe\u0301/re\u0301sume\u0301.txt" // e followed by a combining accent
	)

	if composed == decomposed {
		t.Fatal("test inputs are identical; the test would prove nothing")
	}

	a, err := ParsePath(composed)
	if err != nil {
		t.Fatalf("composed: %v", err)
	}
	b, err := ParsePath(decomposed)
	if err != nil {
		t.Fatalf("decomposed: %v", err)
	}

	if a != b {
		t.Fatalf("normalisation failed: %q != %q", a, b)
	}
	// Paths are comparable, which is what lets them be used as map and
	// database keys without a helper.
	if a.String() != b.String() {
		t.Fatalf("string forms differ: %q vs %q", a, b)
	}
}

func TestPathParts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in     string
		name   string
		parent string
		isRoot bool
	}{
		{"/", "", "/", true},
		{"/a", "a", "/", false},
		{"/a/b", "b", "/a", false},
		{"/a/b/c.iso", "c.iso", "/a/b", false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			p := MustParsePath(tc.in)
			if got := p.Name(); got != tc.name {
				t.Errorf("Name() = %q, want %q", got, tc.name)
			}
			if got := p.Parent().String(); got != tc.parent {
				t.Errorf("Parent() = %q, want %q", got, tc.parent)
			}
			if got := p.IsRoot(); got != tc.isRoot {
				t.Errorf("IsRoot() = %v, want %v", got, tc.isRoot)
			}
		})
	}
}

// TestPathChildRejectsSeparator guards the path by which a directory entry read
// from disk becomes a Path. A crafted name must not be able to inject a
// separator and address a different directory.
func TestPathChildRejectsSeparator(t *testing.T) {
	t.Parallel()

	parent := MustParsePath("/uploads")
	for _, name := range []string{"a/b", "..", ".", "", "../escape", "a\x00b"} {
		if got, err := parent.Child(name); err == nil {
			t.Errorf("Child(%q) = %q, want error", name, got)
		}
	}

	child, err := parent.Child("re\u0301sume\u0301.pdf") // decomposed input
	if err != nil {
		t.Fatalf("Child on a valid name failed: %v", err)
	}
	if want := "/uploads/r\u00e9sum\u00e9.pdf"; child.String() != want { // composed output
		t.Fatalf("Child = %q, want %q (normalised)", child, want)
	}
}
