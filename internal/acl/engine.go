package acl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/db/sqlcgen"
	"github.com/krishna2206/zefile/internal/storage"
)

// Engine resolves permissions against the database.
type Engine struct {
	database *db.DB
	reads    *sqlcgen.Queries
	writes   *sqlcgen.Queries
	now      func() time.Time
}

var (
	_ storage.Guard     = (*Engine)(nil)
	_ storage.ListGuard = (*Engine)(nil)
)

// New builds an Engine over an open database.
func New(database *db.DB) *Engine {
	return &Engine{
		database: database,
		reads:    sqlcgen.New(database.Read),
		writes:   sqlcgen.New(database.Write),
		now:      time.Now,
	}
}

// rule is one applicable grant or denial, flattened from the database.
type rule struct {
	path      string
	perms     Perm
	recursive bool
	deny      bool
}

// Effective returns the permissions a subject holds at a path.
//
// It is exported because the interface needs it: the permissions screen has to
// show what someone can actually do, not merely which rules exist.
func (e *Engine) Effective(ctx context.Context, s Subject, p storage.Path) (Perm, error) {
	perms, err := e.effectiveMany(ctx, s, []storage.Path{p})
	if err != nil {
		return PermNone, err
	}
	return perms[0], nil
}

// Allows reports whether the context's subject holds every bit in want at a
// path. It is the check for capabilities that are not filesystem operations —
// sharing, managing permissions — so they cannot go through the storage Guard.
// An admin always passes; an anonymous context never does.
func (e *Engine) Allows(ctx context.Context, want Perm, p storage.Path) (bool, error) {
	subject, ok := FromContext(ctx)
	if !ok {
		return false, nil
	}
	if subject.IsAdmin {
		return true, nil
	}
	held, err := e.Effective(ctx, subject, p)
	if err != nil {
		return false, err
	}
	return held.Has(want), nil
}

// EffectivePerms returns what the context's subject can do at a path, with the
// admin override applied — what the interface should show and gate actions on.
// Unlike [Engine.Effective], which resolves only written rules, this answers
// PermAll for an administrator, who holds no rules but may do anything.
func (e *Engine) EffectivePerms(ctx context.Context, p storage.Path) (Perm, error) {
	subject, ok := FromContext(ctx)
	if !ok {
		return PermNone, nil
	}
	if subject.IsAdmin {
		return PermAll, nil
	}
	return e.Effective(ctx, subject, p)
}

// Authorize implements [storage.Guard].
func (e *Engine) Authorize(ctx context.Context, op storage.Op, p storage.Path) error {
	verdicts, err := e.Permitted(ctx, op, []storage.Path{p})
	if err != nil {
		return err
	}
	if !verdicts[0] {
		return storage.ErrPermission
	}
	return nil
}

// CanList implements [storage.ListGuard]: a directory may be listed if the
// subject can read it, or if it leads to something they can read. The root is
// always listable — its contents are still filtered per entry, so this reveals
// nothing beyond what CanList's own filtering would.
func (e *Engine) CanList(ctx context.Context, dir storage.Path) (bool, error) {
	subject, ok := FromContext(ctx)
	if !ok {
		return false, nil
	}
	if subject.IsAdmin || dir.IsRoot() {
		return true, nil
	}
	rules, err := e.loadRules(ctx, subject)
	if err != nil {
		return false, err
	}
	owned, err := e.loadOwnership(ctx, subject, []storage.Path{dir})
	if err != nil {
		return false, err
	}
	if resolve(rules, dir, owned[dir.String()]).Has(PermRead) {
		return true, nil
	}
	return hasReadUnder(rules, dir), nil
}

// VisibleChildren implements [storage.ListGuard]: an entry is shown if it is
// readable, or if it is a directory leading to something readable.
func (e *Engine) VisibleChildren(ctx context.Context, paths []storage.Path) ([]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	subject, ok := FromContext(ctx)
	if !ok {
		return make([]bool, len(paths)), nil
	}
	if subject.IsAdmin {
		return allTrue(len(paths)), nil
	}
	rules, err := e.loadRules(ctx, subject)
	if err != nil {
		return nil, err
	}
	owned, err := e.loadOwnership(ctx, subject, paths)
	if err != nil {
		return nil, err
	}
	out := make([]bool, len(paths))
	for i, p := range paths {
		if resolve(rules, p, owned[p.String()]).Has(PermRead) || hasReadUnder(rules, p) {
			out[i] = true
		}
	}
	return out, nil
}

// hasReadUnder reports whether any rule grants read at a path strictly below x —
// that is, whether x is an ancestor of somewhere the subject can read, and so
// worth showing as a way to get there. A denial deeper down is not consulted:
// showing a folder that turns out to hold nothing readable is a harmless quirk,
// where hiding one that leads to a grant is a bug.
func hasReadUnder(rules []rule, x storage.Path) bool {
	xs := x.String()
	prefix := xs
	if prefix != "/" {
		prefix += "/"
	}
	for _, r := range rules {
		if r.deny || !r.perms.Has(PermRead) || r.path == xs {
			continue
		}
		if prefix == "/" || strings.HasPrefix(r.path, prefix) {
			return true
		}
	}
	return false
}

// Permitted implements [storage.Guard].
func (e *Engine) Permitted(ctx context.Context, op storage.Op, paths []storage.Path) ([]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	subject, ok := FromContext(ctx)
	if !ok {
		// Anonymous. Refusing here rather than returning an error keeps the
		// distinction between "not signed in" and "signed in but denied" out of
		// the storage layer, which has no business knowing it.
		return make([]bool, len(paths)), nil
	}
	if subject.IsAdmin {
		return allTrue(len(paths)), nil
	}

	want, err := permForOp(op)
	if err != nil {
		return nil, err
	}

	effective, err := e.effectiveMany(ctx, subject, paths)
	if err != nil {
		return nil, err
	}

	verdicts := make([]bool, len(paths))
	for i, held := range effective {
		verdicts[i] = held.Has(want)
	}
	return verdicts, nil
}

// effectiveMany resolves several paths against one load of the subject's rules.
//
// Rules and ownership are fetched once for the whole batch, which is what makes
// listing a large directory affordable: two queries regardless of how many
// entries it holds.
func (e *Engine) effectiveMany(ctx context.Context, s Subject, paths []storage.Path) ([]Perm, error) {
	rules, err := e.loadRules(ctx, s)
	if err != nil {
		return nil, err
	}
	owned, err := e.loadOwnership(ctx, s, paths)
	if err != nil {
		return nil, err
	}

	out := make([]Perm, len(paths))
	for i, p := range paths {
		out[i] = resolve(rules, p, owned[p.String()])
	}
	return out, nil
}

// loadRules fetches every rule that applies to the subject directly or through
// a group.
//
// All of them are fetched rather than only those matching the requested paths.
// A person has a handful of rules; filtering by path in SQL would mean building
// an IN clause of every ancestor of every path in the batch, which is more
// query than it saves.
func (e *Engine) loadRules(ctx context.Context, s Subject) ([]rule, error) {
	rows, err := e.reads.ListACLForUser(ctx, s.UserID)
	if err != nil {
		return nil, fmt.Errorf("acl: load user rules: %w", err)
	}

	if len(s.Groups) > 0 {
		groupRows, err := e.reads.ListACLForGroups(ctx, s.Groups)
		if err != nil {
			return nil, fmt.Errorf("acl: load group rules: %w", err)
		}
		rows = append(rows, groupRows...)
	}

	rules := make([]rule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, rule{
			path:      row.Path,
			perms:     permFromStored(row.Perms),
			recursive: row.Recursive == 1,
			deny:      row.Deny == 1,
		})
	}
	return rules, nil
}

// loadOwnership reports which of the paths the subject uploaded.
func (e *Engine) loadOwnership(ctx context.Context, s Subject, paths []storage.Path) (map[string]bool, error) {
	keys := make([]string, len(paths))
	for i, p := range paths {
		keys[i] = p.String()
	}

	rows, err := e.reads.GetFileOwnersForPaths(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("acl: load ownership: %w", err)
	}

	owned := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.OwnerID == s.UserID {
			owned[row.Path] = true
		}
	}
	return owned, nil
}

// resolve computes the permissions held at one path.
//
// Every bit is decided independently by the deepest rule that mentions it.
// Ownership enters as a rule below the root, so it is overridden by any
// explicit rule — including one an administrator wrote to take a permission
// away from the person who uploaded the file.
func resolve(rules []rule, target storage.Path, owned bool) Perm {
	const ownershipDepth = -1

	var (
		decidedAt [len(permBits)]int
		decided   [len(permBits)]bool
		denied    [len(permBits)]bool
	)

	consider := func(depth int, perms Perm, deny bool) {
		for i, bit := range permBits {
			if !perms.Has(bit) {
				continue
			}
			switch {
			case !decided[i], depth > decidedAt[i]:
				decided[i], decidedAt[i], denied[i] = true, depth, deny
			case depth == decidedAt[i] && deny:
				// A tie between a grant and a denial at the same depth goes to
				// the denial: the cautious reading is the correct one when two
				// rules disagree.
				denied[i] = true
			}
		}
	}

	if owned {
		consider(ownershipDepth, OwnerPerms, false)
	}
	for _, r := range rules {
		depth, ok := appliesTo(r, target)
		if !ok {
			continue
		}
		consider(depth, r.perms, r.deny)
	}

	held := PermNone
	for i, bit := range permBits {
		if decided[i] && !denied[i] {
			held |= bit
		}
	}
	return held
}

// appliesTo reports whether a rule covers the target, and at what depth.
//
// Depth is the number of components in the rule's own path, so a rule at
// /jeux/steam outranks one at /jeux. The root is depth zero.
func appliesTo(r rule, target storage.Path) (int, bool) {
	rulePath := r.path
	targetPath := target.String()

	if rulePath == targetPath {
		return depthOf(rulePath), true
	}
	if !r.recursive {
		// A non-recursive rule covers its own path and nothing beneath it.
		return 0, false
	}

	if rulePath == "/" {
		return 0, true
	}
	// The trailing separator matters: without it, a rule at /jeu would appear
	// to cover /jeux, which is a different directory entirely.
	if strings.HasPrefix(targetPath, rulePath+"/") {
		return depthOf(rulePath), true
	}
	return 0, false
}

func depthOf(p string) int {
	if p == "/" || p == "" {
		return 0
	}
	return strings.Count(p, "/")
}

func permForOp(op storage.Op) (Perm, error) {
	switch op {
	case storage.OpRead:
		return PermRead, nil
	case storage.OpWrite:
		return PermWrite, nil
	case storage.OpDelete:
		return PermDelete, nil
	default:
		// An unmapped operation must never resolve to "no permission needed".
		return PermNone, fmt.Errorf("acl: unknown operation %d", op)
	}
}

func allTrue(n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = true
	}
	return out
}

// ---------------------------------------------------------------- management

// SubjectType distinguishes the two kinds of rule holder.
type SubjectType string

// The rule holders. Share links are capabilities rather than identities and
// are checked by the share subsystem, so they do not appear here.
const (
	SubjectUser  SubjectType = "user"
	SubjectGroup SubjectType = "group"
)

// Rule is a stored permission rule.
type Rule struct {
	ID          int64
	SubjectType SubjectType
	SubjectID   int64
	Path        storage.Path
	Perms       Perm
	Recursive   bool
	Deny        bool
}

// Grant creates or replaces a rule.
//
// A grant and a denial for the same subject and path are distinct rows, so
// denying write does not erase an existing grant of read.
func (e *Engine) Grant(ctx context.Context, r Rule) (Rule, error) {
	if r.Perms == PermNone {
		return Rule{}, errors.New("acl: a rule with no permissions has no meaning")
	}
	if r.Perms&^PermAll != 0 {
		return Rule{}, fmt.Errorf("acl: unknown permission bits in %d", r.Perms)
	}

	row, err := e.writes.UpsertACL(ctx, sqlcgen.UpsertACLParams{
		SubjectType: string(r.SubjectType),
		SubjectID:   r.SubjectID,
		Path:        r.Path.String(),
		Perms:       int64(r.Perms),
		Recursive:   boolToInt(r.Recursive),
		Deny:        boolToInt(r.Deny),
		CreatedAt:   e.now().Unix(),
	})
	if err != nil {
		return Rule{}, fmt.Errorf("acl: store rule: %w", err)
	}
	return toRule(row), nil
}

// Revoke removes a rule by identifier.
func (e *Engine) Revoke(ctx context.Context, id int64) error {
	if err := e.writes.DeleteACL(ctx, id); err != nil {
		return fmt.Errorf("acl: delete rule: %w", err)
	}
	return nil
}

// RevokeAllForSubject removes every rule held by a user or group, called when
// the account or group is deleted.
func (e *Engine) RevokeAllForSubject(ctx context.Context, kind SubjectType, id int64) error {
	if err := e.writes.DeleteACLForSubject(ctx, sqlcgen.DeleteACLForSubjectParams{
		SubjectType: string(kind),
		SubjectID:   id,
	}); err != nil {
		return fmt.Errorf("acl: delete rules for subject: %w", err)
	}
	return nil
}

// RulesAt lists the rules attached to one exact path, for the permissions
// screen.
func (e *Engine) RulesAt(ctx context.Context, p storage.Path) ([]Rule, error) {
	rows, err := e.reads.ListACLForPath(ctx, p.String())
	if err != nil {
		return nil, fmt.Errorf("acl: list rules: %w", err)
	}
	rules := make([]Rule, 0, len(rows))
	for _, row := range rows {
		rules = append(rules, toRule(row))
	}
	return rules, nil
}

// ----------------------------------------------------------------- ownership

// SetOwner records who uploaded a path.
func (e *Engine) SetOwner(ctx context.Context, p storage.Path, userID int64) error {
	if err := e.writes.SetFileOwner(ctx, sqlcgen.SetFileOwnerParams{
		Path:      p.String(),
		OwnerID:   userID,
		CreatedAt: e.now().Unix(),
	}); err != nil {
		return fmt.Errorf("acl: set owner: %w", err)
	}
	return nil
}

// MoveOwner follows a rename, so ownership is not orphaned by an operation the
// application performed itself. It moves the whole subtree: a renamed folder
// keeps ownership of everything inside it, not only its own entry.
func (e *Engine) MoveOwner(ctx context.Context, from, to storage.Path) error {
	if err := e.writes.MoveFileOwner(ctx, sqlcgen.MoveFileOwnerParams{
		ToPath:   to.String(),
		FromPath: from.String(),
	}); err != nil {
		return fmt.Errorf("acl: move owner: %w", err)
	}
	return nil
}

// MoveRules follows a rename, rewriting the ACL rules at a path and everything
// beneath it. Without this, renaming a shared folder would strand its rules on
// the old name and silently revoke everyone's access to it.
func (e *Engine) MoveRules(ctx context.Context, from, to storage.Path) error {
	if err := e.writes.MoveACLSubtree(ctx, sqlcgen.MoveACLSubtreeParams{
		ToPath:   to.String(),
		FromPath: from.String(),
	}); err != nil {
		return fmt.Errorf("acl: move rules: %w", err)
	}
	return nil
}

// ClearOwner forgets who owned a path, after a permanent delete.
func (e *Engine) ClearOwner(ctx context.Context, p storage.Path) error {
	if err := e.writes.DeleteFileOwner(ctx, p.String()); err != nil {
		return fmt.Errorf("acl: clear owner: %w", err)
	}
	return nil
}

// OwnerOf returns who uploaded a path, and whether anyone did. Files placed by
// other means — over SSH, say — have no owner.
func (e *Engine) OwnerOf(ctx context.Context, p storage.Path) (int64, bool, error) {
	row, err := e.reads.GetFileOwner(ctx, p.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("acl: read owner: %w", err)
	}
	return row.OwnerID, true, nil
}

// LoadSubject assembles a subject from an account, reading group membership
// once so that resolution never has to.
func (e *Engine) LoadSubject(ctx context.Context, userID int64, isAdmin bool) (Subject, error) {
	groups, err := e.reads.ListGroupsForUser(ctx, userID)
	if err != nil {
		return Subject{}, fmt.Errorf("acl: load groups: %w", err)
	}
	return Subject{UserID: userID, IsAdmin: isAdmin, Groups: groups}, nil
}

func toRule(row sqlcgen.Acl) Rule {
	// A path already stored is known valid, so a parse failure here would mean
	// the database was edited by hand. Falling back to the root would silently
	// widen the rule, so the zero value is used instead: a rule that matches
	// nothing is the safe reading.
	p, err := storage.ParsePath(row.Path)
	if err != nil {
		p = storage.Path{}
	}
	return Rule{
		ID:          row.ID,
		SubjectType: SubjectType(row.SubjectType),
		SubjectID:   row.SubjectID,
		Path:        p,
		Perms:       permFromStored(row.Perms),
		Recursive:   row.Recursive == 1,
		Deny:        row.Deny == 1,
	}
}

// permFromStored rebuilds a permission set from a stored value, bit by bit.
//
// Only bits this build defines are honoured. A row holding an unknown bit —
// written by a newer version, or by hand — must not have it reinterpreted as
// some other permission, and a negative or oversized value must not wrap into
// one. Rebuilding rather than converting makes both impossible.
func permFromStored(v int64) Perm {
	var out Perm
	for _, bit := range permBits {
		if v&int64(bit) != 0 {
			out |= bit
		}
	}
	return out
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
