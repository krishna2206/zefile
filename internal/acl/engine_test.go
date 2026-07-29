package acl

import (
	"context"
	"errors"
	"testing"

	"github.com/krishna2206/zefile/internal/db"
	"github.com/krishna2206/zefile/internal/storage"
)

type harness struct {
	engine *Engine
	alice  int64
	bob    int64
	admin  int64
	team   int64
}

func newHarness(t *testing.T) *harness {
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
	return h
}

func (h *harness) addUser(t *testing.T, database *db.DB, name string, admin bool) int64 {
	t.Helper()
	res, err := database.Write.ExecContext(t.Context(),
		`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at)
		 VALUES (?, 'x', ?, 0, 0)`, name, boolToInt(admin))
	if err != nil {
		t.Fatalf("insert user %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func (h *harness) addGroup(t *testing.T, database *db.DB, name string) int64 {
	t.Helper()
	res, err := database.Write.ExecContext(t.Context(),
		`INSERT INTO groups (name, created_at) VALUES (?, 0)`, name)
	if err != nil {
		t.Fatalf("insert group %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func (h *harness) grant(t *testing.T, kind SubjectType, id int64, path string, perms Perm, recursive, deny bool) {
	t.Helper()
	if _, err := h.engine.Grant(t.Context(), Rule{
		SubjectType: kind,
		SubjectID:   id,
		Path:        storage.MustParsePath(path),
		Perms:       perms,
		Recursive:   recursive,
		Deny:        deny,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
}

func (h *harness) subject(id int64, admin bool, groups ...int64) Subject {
	return Subject{UserID: id, IsAdmin: admin, Groups: groups}
}

// TestResolution is the completion criterion for this lot: one table covering
// inheritance, specificity, denial, ownership and group combinations.
func TestResolution(t *testing.T) {
	t.Parallel()

	type grant struct {
		kind      SubjectType
		holder    string // "alice", "bob" or "team"
		path      string
		perms     Perm
		recursive bool
		deny      bool
	}

	cases := []struct {
		name   string
		grants []grant
		owns   []string
		path   string
		groups bool // alice belongs to the team
		want   Perm
	}{
		{
			name: "nothing granted means nothing held",
			path: "/jeux/doom.iso",
			want: PermNone,
		},
		{
			name:   "a recursive grant reaches descendants",
			grants: []grant{{SubjectUser, "alice", "/jeux", PermRead, true, false}},
			path:   "/jeux/steam/doom.iso",
			want:   PermRead,
		},
		{
			name:   "a non-recursive grant stops at its own path",
			grants: []grant{{SubjectUser, "alice", "/jeux", PermRead, false, false}},
			path:   "/jeux/doom.iso",
			want:   PermNone,
		},
		{
			name:   "a non-recursive grant still covers its own path",
			grants: []grant{{SubjectUser, "alice", "/jeux", PermRead, false, false}},
			path:   "/jeux",
			want:   PermRead,
		},
		{
			name: "a deeper rule overrides a shallower one",
			grants: []grant{
				{SubjectUser, "alice", "/", PermRead | PermWrite, true, false},
				{SubjectUser, "alice", "/jeux", PermWrite, true, true},
			},
			path: "/jeux/doom.iso",
			want: PermRead,
		},
		{
			name: "a deeper grant overrides a shallower denial",
			grants: []grant{
				{SubjectUser, "alice", "/prive", PermRead, true, true},
				{SubjectUser, "alice", "/prive/partage", PermRead, true, false},
			},
			path: "/prive/partage/note.txt",
			want: PermRead,
		},
		{
			name: "the denial wins a tie at the same depth",
			grants: []grant{
				{SubjectUser, "alice", "/jeux", PermRead, true, false},
				{SubjectUser, "alice", "/jeux", PermRead, true, true},
			},
			path: "/jeux/doom.iso",
			want: PermNone,
		},
		{
			name: "bits are resolved independently",
			grants: []grant{
				{SubjectUser, "alice", "/", PermRead | PermWrite | PermDelete, true, false},
				{SubjectUser, "alice", "/jeux", PermDelete, true, true},
			},
			path: "/jeux/doom.iso",
			want: PermRead | PermWrite,
		},
		{
			name: "a sibling with a shared prefix is not covered",
			// A rule at /jeu must not reach /jeux, which is a different
			// directory whose name merely starts the same way.
			grants: []grant{{SubjectUser, "alice", "/jeu", PermRead, true, false}},
			path:   "/jeux/doom.iso",
			want:   PermNone,
		},
		{
			name:   "another account's grant is not mine",
			grants: []grant{{SubjectUser, "bob", "/jeux", PermRead, true, false}},
			path:   "/jeux/doom.iso",
			want:   PermNone,
		},
		{
			name:   "a group grant applies to its members",
			grants: []grant{{SubjectGroup, "team", "/equipe", PermRead | PermWrite, true, false}},
			groups: true,
			path:   "/equipe/notes.txt",
			want:   PermRead | PermWrite,
		},
		{
			name:   "a group grant does not apply to non-members",
			grants: []grant{{SubjectGroup, "team", "/equipe", PermRead, true, false}},
			groups: false,
			path:   "/equipe/notes.txt",
			want:   PermNone,
		},
		{
			name: "personal and group rules combine",
			grants: []grant{
				{SubjectGroup, "team", "/equipe", PermRead, true, false},
				{SubjectUser, "alice", "/equipe", PermWrite, true, false},
			},
			groups: true,
			path:   "/equipe/notes.txt",
			want:   PermRead | PermWrite,
		},
		{
			name: "a personal denial beats a group grant at the same depth",
			grants: []grant{
				{SubjectGroup, "team", "/equipe", PermRead, true, false},
				{SubjectUser, "alice", "/equipe", PermRead, true, true},
			},
			groups: true,
			path:   "/equipe/notes.txt",
			want:   PermNone,
		},
		{
			name: "a deeper group grant beats a shallower personal denial",
			grants: []grant{
				{SubjectUser, "alice", "/", PermRead, true, true},
				{SubjectGroup, "team", "/equipe", PermRead, true, false},
			},
			groups: true,
			path:   "/equipe/notes.txt",
			want:   PermRead,
		},
		{
			name: "the uploader holds the owner permissions",
			owns: []string{"/uploads/photo.jpg"},
			path: "/uploads/photo.jpg",
			want: OwnerPerms,
		},
		{
			name: "ownership does not extend to siblings",
			owns: []string{"/uploads/photo.jpg"},
			path: "/uploads/autre.jpg",
			want: PermNone,
		},
		{
			name:   "an explicit denial overrides ownership",
			grants: []grant{{SubjectUser, "alice", "/uploads", PermDelete, true, true}},
			owns:   []string{"/uploads/photo.jpg"},
			path:   "/uploads/photo.jpg",
			want:   PermRead | PermWrite | PermShare,
		},
		{
			name:   "an explicit grant adds to ownership",
			grants: []grant{{SubjectUser, "alice", "/uploads", PermManage, true, false}},
			owns:   []string{"/uploads/photo.jpg"},
			path:   "/uploads/photo.jpg",
			want:   OwnerPerms | PermManage,
		},
		{
			name: "ownership never grants management on its own",
			owns: []string{"/uploads/photo.jpg"},
			path: "/uploads/photo.jpg",
			want: OwnerPerms,
		},
		{
			name:   "a rule at the root reaches everything",
			grants: []grant{{SubjectUser, "alice", "/", PermRead, true, false}},
			path:   "/a/b/c/d/e.txt",
			want:   PermRead,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)

			holders := map[string]struct {
				id   int64
				kind SubjectType
			}{
				"alice": {h.alice, SubjectUser},
				"bob":   {h.bob, SubjectUser},
				"team":  {h.team, SubjectGroup},
			}
			for _, g := range tc.grants {
				holder := holders[g.holder]
				h.grant(t, g.kind, holder.id, g.path, g.perms, g.recursive, g.deny)
			}
			for _, owned := range tc.owns {
				if err := h.engine.SetOwner(t.Context(), storage.MustParsePath(owned), h.alice); err != nil {
					t.Fatalf("SetOwner: %v", err)
				}
			}

			subject := h.subject(h.alice, false)
			if tc.groups {
				subject.Groups = []int64{h.team}
			}

			got, err := h.engine.Effective(t.Context(), subject, storage.MustParsePath(tc.path))
			if err != nil {
				t.Fatalf("Effective: %v", err)
			}
			if got != tc.want {
				t.Fatalf("at %s: got %s, want %s", tc.path, got, tc.want)
			}
		})
	}
}

// TestAdminBypassesRules covers the escape hatch: an administrator must be able
// to reach a path even after writing a denial that would otherwise lock
// everyone out, including themselves.
func TestAdminBypassesRules(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.grant(t, SubjectUser, h.admin, "/", PermAll, true, true)

	ctx := NewContext(t.Context(), h.subject(h.admin, true))
	for _, op := range []storage.Op{storage.OpRead, storage.OpWrite, storage.OpDelete} {
		if err := h.engine.Authorize(ctx, op, storage.MustParsePath("/anywhere")); err != nil {
			t.Errorf("%s refused to an administrator: %v", op, err)
		}
	}
}

// TestAnonymousIsRefused checks the default. A context without a subject must
// never resolve to permission.
func TestAnonymousIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.grant(t, SubjectUser, h.alice, "/", PermAll, true, false)

	// No subject in the context: not signed in.
	err := h.engine.Authorize(context.Background(), storage.OpRead, storage.MustParsePath("/jeux"))
	if !errors.Is(err, storage.ErrPermission) {
		t.Fatalf("anonymous access = %v, want ErrPermission", err)
	}
}

func TestAuthorizeMapsOperations(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.grant(t, SubjectUser, h.alice, "/jeux", PermRead, true, false)

	ctx := NewContext(t.Context(), h.subject(h.alice, false))
	p := storage.MustParsePath("/jeux/doom.iso")

	if err := h.engine.Authorize(ctx, storage.OpRead, p); err != nil {
		t.Errorf("read refused despite a read grant: %v", err)
	}
	for _, op := range []storage.Op{storage.OpWrite, storage.OpDelete} {
		if err := h.engine.Authorize(ctx, op, p); !errors.Is(err, storage.ErrPermission) {
			t.Errorf("%s allowed with only a read grant: %v", op, err)
		}
	}
}

// TestUnknownOperationIsRefused guards the mapping itself: a new storage
// operation added without a matching permission must fail closed rather than
// resolve to "nothing required".
func TestUnknownOperationIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.grant(t, SubjectUser, h.alice, "/", PermAll, true, false)

	ctx := NewContext(t.Context(), h.subject(h.alice, false))
	if err := h.engine.Authorize(ctx, storage.Op(200), storage.MustParsePath("/x")); err == nil {
		t.Fatal("an unmapped operation was allowed")
	}
}

func TestPermittedMatchesAuthorize(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.grant(t, SubjectUser, h.alice, "/visible", PermRead, true, false)
	h.grant(t, SubjectUser, h.alice, "/visible/cache", PermRead, true, true)

	ctx := NewContext(t.Context(), h.subject(h.alice, false))
	paths := []storage.Path{
		storage.MustParsePath("/visible/a.txt"),
		storage.MustParsePath("/visible/cache/b.txt"),
		storage.MustParsePath("/ailleurs/c.txt"),
	}

	verdicts, err := h.engine.Permitted(ctx, storage.OpRead, paths)
	if err != nil {
		t.Fatalf("Permitted: %v", err)
	}
	want := []bool{true, false, false}
	for i := range paths {
		if verdicts[i] != want[i] {
			t.Errorf("Permitted(%s) = %v, want %v", paths[i], verdicts[i], want[i])
		}
		// The batch and single answers must never disagree, or a listing would
		// show something that cannot then be opened.
		singleErr := h.engine.Authorize(ctx, storage.OpRead, paths[i])
		if (singleErr == nil) != verdicts[i] {
			t.Errorf("Authorize and Permitted disagree at %s", paths[i])
		}
	}
}

func TestGrantValidation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if _, err := h.engine.Grant(t.Context(), Rule{
		SubjectType: SubjectUser,
		SubjectID:   h.alice,
		Path:        storage.MustParsePath("/x"),
		Perms:       PermNone,
	}); err == nil {
		t.Error("a rule with no permissions was accepted")
	}

	if _, err := h.engine.Grant(t.Context(), Rule{
		SubjectType: SubjectUser,
		SubjectID:   h.alice,
		Path:        storage.MustParsePath("/x"),
		Perms:       Perm(1 << 20),
	}); err == nil {
		t.Error("a rule with unknown permission bits was accepted")
	}
}

func TestGrantReplacesAndRevokes(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()
	p := storage.MustParsePath("/jeux")

	h.grant(t, SubjectUser, h.alice, "/jeux", PermRead, true, false)
	h.grant(t, SubjectUser, h.alice, "/jeux", PermRead|PermWrite, true, false)

	rules, err := h.engine.RulesAt(ctx, p)
	if err != nil {
		t.Fatalf("RulesAt: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want the second grant to have replaced the first", len(rules))
	}
	if rules[0].Perms != PermRead|PermWrite {
		t.Errorf("Perms = %s, want read+write", rules[0].Perms)
	}

	// A grant and a denial are separate rows: denying write must not erase the
	// grant of read.
	h.grant(t, SubjectUser, h.alice, "/jeux", PermWrite, true, true)
	if rules, err = h.engine.RulesAt(ctx, p); err != nil {
		t.Fatalf("RulesAt: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want a grant and a denial side by side", len(rules))
	}

	if err := h.engine.RevokeAllForSubject(ctx, SubjectUser, h.alice); err != nil {
		t.Fatalf("RevokeAllForSubject: %v", err)
	}
	if rules, err = h.engine.RulesAt(ctx, p); err != nil {
		t.Fatalf("RulesAt: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("got %d rules after revoking everything", len(rules))
	}
}

func TestOwnership(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()
	from := storage.MustParsePath("/uploads/a.iso")
	to := storage.MustParsePath("/jeux/a.iso")

	if _, found, err := h.engine.OwnerOf(ctx, from); err != nil || found {
		t.Fatalf("OwnerOf on an unowned path: found=%v err=%v", found, err)
	}

	if err := h.engine.SetOwner(ctx, from, h.alice); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}
	owner, found, err := h.engine.OwnerOf(ctx, from)
	if err != nil || !found || owner != h.alice {
		t.Fatalf("OwnerOf = (%d, %v, %v)", owner, found, err)
	}

	// Ownership is keyed by path, so a rename the application performs must
	// carry it across or the record is orphaned.
	if err := h.engine.MoveOwner(ctx, from, to); err != nil {
		t.Fatalf("MoveOwner: %v", err)
	}
	if _, found, _ := h.engine.OwnerOf(ctx, from); found {
		t.Error("ownership stayed on the old path")
	}
	if owner, found, _ = h.engine.OwnerOf(ctx, to); !found || owner != h.alice {
		t.Error("ownership did not follow the move")
	}

	if err := h.engine.ClearOwner(ctx, to); err != nil {
		t.Fatalf("ClearOwner: %v", err)
	}
	if _, found, _ := h.engine.OwnerOf(ctx, to); found {
		t.Error("ownership survived ClearOwner")
	}
}

func TestLoadSubjectReadsGroups(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := t.Context()

	subject, err := h.engine.LoadSubject(ctx, h.alice, false)
	if err != nil {
		t.Fatalf("LoadSubject: %v", err)
	}
	if len(subject.Groups) != 0 {
		t.Errorf("Groups = %v, want empty", subject.Groups)
	}

	if _, err := h.engine.database.Write.ExecContext(ctx,
		`INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, h.team, h.alice); err != nil {
		t.Fatalf("add membership: %v", err)
	}

	if subject, err = h.engine.LoadSubject(ctx, h.alice, false); err != nil {
		t.Fatalf("LoadSubject: %v", err)
	}
	if len(subject.Groups) != 1 || subject.Groups[0] != h.team {
		t.Errorf("Groups = %v, want [%d]", subject.Groups, h.team)
	}
}

// TestStoredPermissionsAreRebuiltNotReinterpreted covers a row holding bits
// this build does not define — written by a newer version, or by hand. An
// unknown bit must be dropped, never folded into a permission that happens to
// share its position after a conversion.
func TestStoredPermissionsAreRebuiltNotReinterpreted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stored int64
		want   Perm
	}{
		{"exact set", int64(PermRead | PermWrite), PermRead | PermWrite},
		{"unknown high bit", int64(PermRead) | 1<<20, PermRead},
		{"beyond uint32", int64(PermRead) | 1<<40, PermRead},
		{"negative", -1, PermAll},
		{"zero", 0, PermNone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := permFromStored(tc.stored); got != tc.want {
				t.Fatalf("permFromStored(%d) = %s, want %s", tc.stored, got, tc.want)
			}
		})
	}
}
