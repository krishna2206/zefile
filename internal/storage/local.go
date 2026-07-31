package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"
)

// spaceCacheTTL bounds how often free space is measured. A statfs per write
// would be wasteful during a large upload, and the reserve is generous enough
// that a couple of seconds of staleness cannot fill the disk.
const spaceCacheTTL = 2 * time.Second

// Config configures a [Local].
type Config struct {
	// Root is the host directory that becomes the top of the tree. Required.
	Root string

	// Guard authorises operations. Nil means [AllowAll], which is correct for
	// the single-user phase and for tests, and wrong for anything else.
	Guard Guard

	// Reserve is the free space kept in hand before refusing writes. Zero
	// selects [DefaultReserve]; use a negative-free instance only in tests.
	Reserve uint64

	// ReadOnly refuses every write regardless of free space.
	ReadOnly bool
}

// Local is an [FS] backed by a directory on the host filesystem.
type Local struct {
	root     *os.Root
	guard    Guard
	reserve  uint64
	readOnly bool

	mu        sync.Mutex
	cached    SpaceInfo
	cachedAt  time.Time
	cachedErr error
}

var _ FS = (*Local)(nil)

// Open confines a Local to the given host directory.
//
// The directory must already exist: creating it here would mean a typo in the
// configuration silently produces an empty instance rather than an error.
func Open(cfg Config) (*Local, error) {
	if cfg.Root == "" {
		return nil, errors.New("storage: root is required")
	}
	info, err := os.Stat(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("storage: root %q: %w", cfg.Root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("storage: root %q is not a directory", cfg.Root)
	}

	root, err := os.OpenRoot(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("storage: open root %q: %w", cfg.Root, err)
	}

	guard := cfg.Guard
	if guard == nil {
		guard = AllowAll{}
	}
	reserve := cfg.Reserve
	if reserve == 0 {
		reserve = DefaultReserve
	}

	return &Local{
		root:     root,
		guard:    guard,
		reserve:  reserve,
		readOnly: cfg.ReadOnly,
	}, nil
}

// Close releases the root handle.
func (l *Local) Close() error { return l.root.Close() }

// HostRoot returns the host directory backing this storage, for diagnostics.
func (l *Local) HostRoot() string { return l.root.Name() }

// ---------------------------------------------------------------- read paths

// Stat implements [FS].
func (l *Local) Stat(ctx context.Context, p Path) (FileInfo, error) {
	if err := l.access(ctx, OpRead, p); err != nil {
		return FileInfo{}, err
	}
	_, info, err := l.resolveExisting(p)
	if err != nil {
		return FileInfo{}, err
	}
	return toFileInfo(p, info), nil
}

// List implements [FS].
func (l *Local) List(ctx context.Context, p Path) ([]FileInfo, error) {
	// The reserved check comes first, as in access: a caller must not learn that
	// a reserved path exists by observing which error comes back.
	if isReserved(p) {
		return nil, ErrReserved
	}

	// A ListGuard lets someone list a directory that only leads to what they were
	// granted; without one, listing needs plain read on the directory.
	lister, traverse := l.guard.(ListGuard)
	if traverse {
		ok, err := lister.CanList(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("storage: authorisation failed: %w", err)
		}
		if !ok {
			return nil, ErrPermission
		}
	} else if err := l.guard.Authorize(ctx, OpRead, p); err != nil {
		if errors.Is(err, ErrPermission) {
			return nil, ErrPermission
		}
		return nil, fmt.Errorf("storage: authorisation failed: %w", err)
	}

	rel, info, err := l.resolveExisting(p)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrNotDir
	}

	candidates, err := l.readDir(p, rel)
	if err != nil {
		return nil, err
	}

	if traverse {
		return l.filterVisible(ctx, lister, candidates)
	}
	return l.filterReadable(ctx, candidates)
}

// readDir reads a directory's entries into FileInfos, dropping reserved names at
// the root and entries that vanish or cannot form a valid path.
func (l *Local) readDir(p Path, rel string) ([]FileInfo, error) {
	dir, err := l.root.Open(rel)
	if err != nil {
		return nil, mapErr(err)
	}
	defer dir.Close()

	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, mapErr(err)
	}

	candidates := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		if p.IsRoot() && isReservedName(entry.Name()) {
			continue
		}
		child, err := p.Child(entry.Name())
		if err != nil {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, mapErr(err)
		}
		candidates = append(candidates, toFileInfo(child, entryInfo))
	}
	return candidates, nil
}

// filterVisible keeps the entries a ListGuard says the caller may see: those
// readable, plus directories that lead to something readable.
func (l *Local) filterVisible(ctx context.Context, lister ListGuard, entries []FileInfo) ([]FileInfo, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	paths := make([]Path, len(entries))
	for i, entry := range entries {
		paths[i] = entry.Path
	}

	verdicts, err := lister.VisibleChildren(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("storage: authorisation failed: %w", err)
	}
	if len(verdicts) != len(entries) {
		return nil, fmt.Errorf("storage: guard returned %d verdicts for %d paths", len(verdicts), len(entries))
	}

	visible := entries[:0]
	for i, ok := range verdicts {
		if ok {
			visible = append(visible, entries[i])
		}
	}
	return visible, nil
}

// filterReadable drops entries the caller may not see.
//
// Without this, a listing reveals the names of everything in a directory even
// where a rule denies access to some of it — and a filename is often the whole
// secret. The verdicts are fetched in one batch: a directory can hold tens of
// thousands of entries, and a query each would make listing unusable.
func (l *Local) filterReadable(ctx context.Context, entries []FileInfo) ([]FileInfo, error) {
	if len(entries) == 0 {
		return entries, nil
	}

	paths := make([]Path, len(entries))
	for i, entry := range entries {
		paths[i] = entry.Path
	}

	verdicts, err := l.guard.Permitted(ctx, OpRead, paths)
	if err != nil {
		return nil, fmt.Errorf("storage: authorisation failed: %w", err)
	}
	if len(verdicts) != len(entries) {
		// A Guard returning a mismatched result cannot be interpreted, and
		// guessing would mean either leaking entries or hiding legitimate ones.
		return nil, fmt.Errorf("storage: guard returned %d verdicts for %d paths", len(verdicts), len(entries))
	}

	visible := entries[:0]
	for i, ok := range verdicts {
		if ok {
			visible = append(visible, entries[i])
		}
	}
	return visible, nil
}

// Open implements [FS].
func (l *Local) Open(ctx context.Context, p Path) (File, error) {
	if err := l.access(ctx, OpRead, p); err != nil {
		return nil, err
	}
	rel, info, err := l.resolveExisting(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, ErrIsDir
	}
	f, err := l.root.Open(rel)
	if err != nil {
		return nil, mapErr(err)
	}
	return f, nil
}

// --------------------------------------------------------------- write paths

// Create implements [FS].
func (l *Local) Create(ctx context.Context, p Path) (WriteFile, error) {
	if p.IsRoot() {
		return nil, ErrIsDir
	}
	if err := l.access(ctx, OpWrite, p); err != nil {
		return nil, err
	}
	if err := l.checkWritable(); err != nil {
		return nil, err
	}
	rel, err := l.resolveForCreate(p)
	if err != nil {
		return nil, err
	}
	f, err := l.root.OpenFile(rel, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, mapErr(err)
	}
	return f, nil
}

// Mkdir implements [FS].
func (l *Local) Mkdir(ctx context.Context, p Path) error {
	if p.IsRoot() {
		return ErrExist
	}
	if err := l.access(ctx, OpWrite, p); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}
	rel, err := l.resolveForCreate(p)
	if err != nil {
		return err
	}
	return mapErr(l.root.Mkdir(rel, 0o755))
}

// MkdirAll implements [FS].
func (l *Local) MkdirAll(ctx context.Context, p Path) error {
	if p.IsRoot() {
		return nil
	}
	if err := l.access(ctx, OpWrite, p); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}
	// Existing ancestors may be stored in a different normal form, so resolve
	// as far as the tree already goes and only create what is genuinely absent.
	rel, err := l.resolveForMkdirAll(p)
	if err != nil {
		return err
	}
	return mapErr(l.root.MkdirAll(rel, 0o755))
}

// Move implements [FS].
func (l *Local) Move(ctx context.Context, from, to Path) error {
	if from.IsRoot() || to.IsRoot() {
		return fmt.Errorf("%w: cannot move the root", ErrInvalidPath)
	}
	// Moving out of a directory removes the entry from it, so the source needs
	// delete rights — read plus write on the destination is not enough.
	if err := l.access(ctx, OpDelete, from); err != nil {
		return err
	}
	if err := l.access(ctx, OpWrite, to); err != nil {
		return err
	}
	if err := l.checkWritable(); err != nil {
		return err
	}
	if strings.HasPrefix(to.String()+"/", from.String()+"/") {
		return fmt.Errorf("%w: cannot move a directory into itself", ErrInvalidPath)
	}

	fromRel, _, err := l.resolveExisting(from)
	if err != nil {
		return err
	}
	toRel, err := l.resolveForCreate(to)
	if err != nil {
		return err
	}
	return mapErr(l.root.Rename(fromRel, toRel))
}

// Remove implements [FS].
func (l *Local) Remove(ctx context.Context, p Path) error {
	if p.IsRoot() {
		return fmt.Errorf("%w: cannot remove the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpDelete, p); err != nil {
		return err
	}
	if err := l.checkDeletable(); err != nil {
		return err
	}
	rel, _, err := l.resolveExisting(p)
	if err != nil {
		return err
	}
	return mapErr(l.root.Remove(rel))
}

// RemoveAll implements [FS].
func (l *Local) RemoveAll(ctx context.Context, p Path) error {
	if p.IsRoot() {
		return fmt.Errorf("%w: cannot remove the root", ErrInvalidPath)
	}
	if err := l.access(ctx, OpDelete, p); err != nil {
		return err
	}
	if err := l.checkDeletable(); err != nil {
		return err
	}
	rel, _, err := l.resolveExisting(p)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil // RemoveAll is idempotent, like os.RemoveAll.
		}
		return err
	}
	return mapErr(l.root.RemoveAll(rel))
}

// ---------------------------------------------------------------------- gates

// access runs the reserved-name check and the Guard, in that order: a caller
// must not learn whether a reserved path exists by observing which error comes
// back first.
func (l *Local) access(ctx context.Context, op Op, p Path) error {
	if isReserved(p) {
		return ErrReserved
	}
	if err := l.guard.Authorize(ctx, op, p); err != nil {
		if errors.Is(err, ErrPermission) {
			return ErrPermission
		}
		// A Guard that failed to decide must not fall open.
		return fmt.Errorf("storage: authorisation failed: %w", err)
	}
	return nil
}

// checkWritable refuses operations that consume space.
func (l *Local) checkWritable() error {
	if l.readOnly {
		return ErrReadOnly
	}
	info, err := l.Space(context.Background())
	if err != nil {
		// Free space could not be measured. Refusing every write because of a
		// failed statfs would take the instance down over a diagnostic; the
		// filesystem still reports ENOSPC if it really is full.
		return nil
	}
	if info.Available < info.Reserve {
		return ErrNoSpace
	}
	return nil
}

// checkDeletable gates operations that free space. Deletion stays available
// when the disk is full — that is the whole point of degrading to read-only
// rather than failing outright — but is still refused in configured
// read-only mode.
func (l *Local) checkDeletable() error {
	if l.readOnly {
		return ErrReadOnly
	}
	return nil
}

// Space implements [FS].
func (l *Local) Space(context.Context) (SpaceInfo, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.cachedAt.IsZero() && time.Since(l.cachedAt) < spaceCacheTTL {
		return l.cached, l.cachedErr
	}

	avail, total, err := availableSpace(l.root.Name())
	l.cachedAt = time.Now()
	if err != nil {
		l.cached, l.cachedErr = SpaceInfo{}, err
		return l.cached, l.cachedErr
	}

	l.cached = SpaceInfo{
		Available: avail,
		Total:     total,
		Reserve:   l.reserve,
		ReadOnly:  l.readOnly || avail < l.reserve,
	}
	l.cachedErr = nil
	return l.cached, nil
}

// ----------------------------------------------------------------- resolution

// resolveExisting maps a key-form path to the name actually on disk.
//
// The direct lookup succeeds for everything Zefile created, so the fallback
// only runs for files placed by other means — over SSH, or by a macOS client
// that wrote a decomposed name.
func (l *Local) resolveExisting(p Path) (string, os.FileInfo, error) {
	rel := p.rel()
	if info, err := l.root.Lstat(rel); err == nil {
		return rel, info, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", nil, mapErr(err)
	}

	rel, matched, err := l.resolvePrefix(p)
	if err != nil {
		return "", nil, err
	}
	if matched != len(p.components()) {
		return "", nil, ErrNotExist
	}
	info, err := l.root.Lstat(rel)
	if err != nil {
		return "", nil, mapErr(err)
	}
	return rel, info, nil
}

// resolveForMkdirAll maps a key-form path to a disk path where any existing
// ancestors keep their on-disk spelling and the missing tail is written in NFC.
//
// It cannot go through resolveForCreate: MkdirAll exists precisely for the case
// where the parents are absent.
func (l *Local) resolveForMkdirAll(p Path) (string, error) {
	rel, matched, err := l.resolvePrefix(p)
	if err != nil {
		return "", err
	}
	for _, comp := range p.components()[matched:] {
		if rel == "." {
			rel = comp
		} else {
			rel += "/" + comp
		}
	}
	return rel, nil
}

// resolveForCreate maps a key-form path to where a new entry should be written:
// the parent as it exists on disk, plus the requested name in key form.
//
// New names are always written in NFC. Zefile's own tree is therefore uniformly
// normalised, and the fallback above stays a rare path rather than the norm.
func (l *Local) resolveForCreate(p Path) (string, error) {
	parent := p.Parent()
	if parent.IsRoot() {
		return p.Name(), nil
	}
	parentRel, info, err := l.resolveExisting(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", ErrNotDir
	}
	return path.Join(parentRel, p.Name()), nil
}

// resolvePrefix walks the path component by component, translating each to its
// on-disk spelling, and stops at the first component that does not exist.
//
// It returns the resolved prefix and how many components were matched, which
// lets one walk serve both "everything must exist" and "create what is
// missing".
func (l *Local) resolvePrefix(p Path) (rel string, matched int, err error) {
	cur := "."
	for i, want := range p.components() {
		candidate := want
		if cur != "." {
			candidate = cur + "/" + want
		}
		if _, err := l.root.Lstat(candidate); err == nil {
			cur = candidate
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", i, mapErr(err)
		}

		match, err := l.findByNormalisation(cur, want)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				return cur, i, nil
			}
			return "", i, err
		}
		if cur == "." {
			cur = match
		} else {
			cur = cur + "/" + match
		}
	}
	return cur, len(p.components()), nil
}

// findByNormalisation scans a directory for the entry whose normalised name
// equals want.
//
// Two entries can normalise to the same key — a directory holding both the
// composed and decomposed spelling of one name. That is ambiguous, and picking
// one would mean acting on a file the caller did not ask for, so it is an
// error instead.
func (l *Local) findByNormalisation(dir, want string) (string, error) {
	d, err := l.root.Open(dir)
	if err != nil {
		return "", mapErr(err)
	}
	defer d.Close()

	names, err := d.Readdirnames(-1)
	if err != nil {
		return "", mapErr(err)
	}

	found := ""
	for _, name := range names {
		if norm.NFC.String(name) != want {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("%w: %q in %q", ErrAmbiguous, want, dir)
		}
		found = name
	}
	if found == "" {
		return "", ErrNotExist
	}
	return found, nil
}

// --------------------------------------------------------------------- helpers

func isReservedName(name string) bool {
	return name == TrashDir || name == UploadsDir
}

func isReserved(p Path) bool {
	comps := p.components()
	return len(comps) > 0 && isReservedName(comps[0])
}

func toFileInfo(p Path, info os.FileInfo) FileInfo {
	return FileInfo{
		Path:    p,
		Name:    p.Name(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		Symlink: info.Mode()&os.ModeSymlink != 0,
	}
}

// mapErr translates filesystem errors into this package's sentinels so callers
// never have to reason about syscall numbers or *PathError wrappers.
//
// Errors it does not recognise are returned unchanged, including the escape
// errors from os.Root: those mean an attempt to leave the tree and deserve to
// surface as themselves rather than be flattened into "not found".
func mapErr(err error) error {
	// Specific errno checks come first, deliberately. syscall.Errno.Is reports
	// ENOTEMPTY as matching fs.ErrExist, so testing the general case first
	// would turn "directory not empty" into "already exists" and send the
	// caller looking for a name collision that does not exist.
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ENOTEMPTY):
		return ErrNotEmpty
	case errors.Is(err, syscall.ENOTDIR):
		return ErrNotDir
	case errors.Is(err, syscall.EISDIR):
		return ErrIsDir
	case errors.Is(err, syscall.EXDEV):
		return ErrCrossDevice
	case errors.Is(err, syscall.ENOSPC):
		return ErrNoSpace
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotExist
	case errors.Is(err, fs.ErrExist):
		return ErrExist
	case errors.Is(err, fs.ErrPermission):
		return ErrPermission
	}
	return err
}

// ContainsPath reports whether other lies inside root, following symlinks on
// both sides.
//
// It backs the startup rule that the configuration directory must not sit
// inside the storage tree: a SQLite database that users can list is a database
// they can download, password hashes included.
func ContainsPath(root, other string) (bool, error) {
	rootReal, err := realPath(root)
	if err != nil {
		return false, fmt.Errorf("storage: resolve %q: %w", root, err)
	}
	otherReal, err := realPath(other)
	if err != nil {
		return false, fmt.Errorf("storage: resolve %q: %w", other, err)
	}

	rel, err := filepath.Rel(rootReal, otherReal)
	if err != nil {
		return false, nil // Different volumes: not contained.
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// realPath resolves symlinks as far down as the path actually exists, then
// appends the remainder unchanged.
//
// filepath.EvalSymlinks fails outright on a path that does not exist yet, and
// falling back to filepath.Abs would compare a resolved path against an
// unresolved one. On macOS that alone breaks the comparison, since /var is a
// symlink to /private/var — the containment check would quietly answer "no"
// for a directory that has not been created yet.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}

	cur, rest := abs, ""
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the volume root without finding anything that exists.
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
