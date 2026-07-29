package storage

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Limits on what a path may look like. These are deliberately stricter than any
// filesystem requires: a name Zefile refuses is a name that cannot surprise us
// later, and no legitimate file needs a control character in it.
const (
	// MaxComponentBytes is the per-component limit, matching the common
	// filesystem maximum so a path we accept is a path we can create.
	MaxComponentBytes = 255

	// MaxPathBytes bounds the whole path, matching the usual PATH_MAX.
	MaxPathBytes = 4096
)

// Path is a validated, NFC-normalised virtual path, always rooted at "/" and
// always slash-separated regardless of the host operating system.
//
// The field is unexported so a Path can only come from [ParsePath]. A raw
// string can therefore never reach a syscall by accident, which is the point:
// the type system carries the guarantee rather than a code review.
type Path struct {
	p string
}

// Root is the top of the storage tree.
var Root = Path{p: "/"}

// ParsePath validates and normalises a client-supplied path.
//
// It is strict by design and does not silently repair its input. A path
// containing "." or ".." is rejected rather than resolved, so that the path
// recorded in an ACL entry is exactly the path the client asked for. Callers
// get a clear error instead of an operation that quietly acted somewhere else.
func ParsePath(s string) (Path, error) {
	if s == "" {
		return Path{}, fmt.Errorf("%w: empty", ErrInvalidPath)
	}
	if len(s) > MaxPathBytes {
		return Path{}, fmt.Errorf("%w: longer than %d bytes", ErrInvalidPath, MaxPathBytes)
	}
	if !utf8.ValidString(s) {
		return Path{}, fmt.Errorf("%w: not valid UTF-8", ErrInvalidPath)
	}
	if s[0] != '/' {
		return Path{}, fmt.Errorf("%w: must start with %q", ErrInvalidPath, "/")
	}

	// Normalise before splitting. NFC leaves the ASCII separator untouched, so
	// component boundaries cannot move, but it means every later comparison is
	// against one canonical form.
	s = norm.NFC.String(s)

	// A single trailing slash is a common way to denote a directory and is
	// harmless; anything beyond that is malformed.
	if len(s) > 1 {
		s = strings.TrimSuffix(s, "/")
	}
	if s == "" || s == "/" {
		return Root, nil
	}

	for _, comp := range strings.Split(s[1:], "/") {
		if err := validateComponent(comp); err != nil {
			return Path{}, err
		}
	}
	return Path{p: s}, nil
}

// MustParsePath is ParsePath for constants known to be valid. It panics on
// invalid input and must never be used on anything a client can influence.
func MustParsePath(s string) Path {
	p, err := ParsePath(s)
	if err != nil {
		panic(err)
	}
	return p
}

func validateComponent(comp string) error {
	switch comp {
	case "":
		return fmt.Errorf("%w: empty component", ErrInvalidPath)
	case ".", "..":
		// os.Root would refuse to escape anyway. Rejecting here turns a
		// confusing "not found" into an explicit, diagnosable error.
		return fmt.Errorf("%w: %q component", ErrInvalidPath, comp)
	}
	if len(comp) > MaxComponentBytes {
		return fmt.Errorf("%w: component longer than %d bytes", ErrInvalidPath, MaxComponentBytes)
	}
	for _, r := range comp {
		// Control characters, including NUL, break shells, HTTP headers and
		// terminals in ways ranging from annoying to exploitable.
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: control character in component", ErrInvalidPath)
		}
	}
	return nil
}

// String returns the path in key form: NFC-normalised, slash-separated.
func (p Path) String() string { return p.p }

// IsRoot reports whether p is the top of the tree.
func (p Path) IsRoot() bool { return p.p == "/" || p.p == "" }

// Name returns the final component, or "" for the root.
func (p Path) Name() string {
	if p.IsRoot() {
		return ""
	}
	if i := strings.LastIndexByte(p.p, '/'); i >= 0 {
		return p.p[i+1:]
	}
	return p.p
}

// Parent returns the containing directory. The parent of the root is itself.
func (p Path) Parent() Path {
	if p.IsRoot() {
		return Root
	}
	i := strings.LastIndexByte(p.p, '/')
	if i <= 0 {
		return Root
	}
	return Path{p: p.p[:i]}
}

// Child returns the path of an entry directly inside p.
//
// The name is validated, so a directory entry read from disk cannot inject a
// separator into a path we then act on.
func (p Path) Child(name string) (Path, error) {
	name = norm.NFC.String(name)
	if strings.ContainsRune(name, '/') {
		return Path{}, fmt.Errorf("%w: separator in name %q", ErrInvalidPath, name)
	}
	if err := validateComponent(name); err != nil {
		return Path{}, err
	}
	if p.IsRoot() {
		return Path{p: "/" + name}, nil
	}
	child := p.p + "/" + name
	if len(child) > MaxPathBytes {
		return Path{}, fmt.Errorf("%w: longer than %d bytes", ErrInvalidPath, MaxPathBytes)
	}
	return Path{p: child}, nil
}

// components returns the path split into its parts, empty for the root.
func (p Path) components() []string {
	if p.IsRoot() {
		return nil
	}
	return strings.Split(p.p[1:], "/")
}

// rel returns the path as os.Root expects it: relative, with the root itself
// expressed as ".".
func (p Path) rel() string {
	if p.IsRoot() {
		return "."
	}
	return p.p[1:]
}
