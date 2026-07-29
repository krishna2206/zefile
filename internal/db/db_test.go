package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T, cfg Config) *DB {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	d, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpenAppliesMigrations(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})

	want := []string{
		"users", "sessions", "api_tokens", "invitations", "groups",
		"group_members", "acl", "file_owners", "shares", "share_access_log",
		"trash", "uploads", "jobs", "audit_log", "file_index",
	}
	for _, table := range want {
		var name string
		err := d.Read.QueryRowContext(t.Context(),
			`SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}

	if err := d.Verify(t.Context()); err != nil {
		t.Errorf("Verify on a fresh database: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := open(t, Config{Dir: dir})

	if _, err := first.Write.ExecContext(t.Context(),
		`INSERT INTO users (username, password_hash, created_at, updated_at)
		 VALUES ('admin', 'x', 1, 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening must migrate to the same place and leave the data alone —
	// this is what a binary upgrade does on a running instance.
	second := open(t, Config{Dir: dir})
	var count int
	if err := second.Read.QueryRowContext(t.Context(), `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("users = %d, want 1 — reopening lost data", count)
	}
}

func TestPragmasAreApplied(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})

	// Both pools are checked. The pragmas travel in the DSN precisely so that
	// every connection gets them, including ones the pool opens later; testing
	// only one pool would not catch a DSN that lost a setting.
	pools := map[string]*sql.DB{"read": d.Read, "write": d.Write}
	cases := []struct{ pragma, want string }{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
	}

	for name, pool := range pools {
		for _, tc := range cases {
			var got string
			if err := pool.QueryRowContext(t.Context(), "PRAGMA "+tc.pragma).Scan(&got); err != nil {
				t.Fatalf("%s pool, PRAGMA %s: %v", name, tc.pragma, err)
			}
			if got != tc.want {
				t.Errorf("%s pool, %s = %q, want %q", name, tc.pragma, got, tc.want)
			}
		}
	}
}

// TestForeignKeysAreEnforced proves the pragma is not merely reported as on.
// Without enforcement every ON DELETE clause in the schema is decoration.
func TestForeignKeysAreEnforced(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})
	ctx := t.Context()

	_, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, created_at, last_seen_at, expires_at)
		 VALUES (999, X'00', 1, 1, 2)`)
	if err == nil {
		t.Fatal("a session was inserted for a user that does not exist")
	}

	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, created_at, updated_at)
		 VALUES (1, 'u', 'x', 1, 1)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, created_at, last_seen_at, expires_at)
		 VALUES (1, X'01', 1, 1, 2)`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var sessions int
	if err := d.Read.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("sessions = %d after deleting the user, want 0", sessions)
	}
}

// TestAuditLogSurvivesUserDeletion is the counterpart: an audit trail that
// disappeared with the account it describes would be worthless.
func TestAuditLogSurvivesUserDeletion(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})
	ctx := t.Context()

	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, created_at, updated_at)
		 VALUES (1, 'u', 'x', 1, 1)`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO audit_log (at, actor_id, action) VALUES (1, 1, 'session.login')`); err != nil {
		t.Fatalf("insert audit row: %v", err)
	}
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var actor *int64
	var action string
	err := d.Read.QueryRowContext(ctx, `SELECT actor_id, action FROM audit_log`).Scan(&actor, &action)
	if err != nil {
		t.Fatalf("the audit entry vanished with the account: %v", err)
	}
	if actor != nil {
		t.Errorf("actor_id = %v, want NULL", *actor)
	}
	if action != "session.login" {
		t.Errorf("action = %q, want it preserved", action)
	}
}

// TestFullTextSearchWorks checks the search index at schema time rather than in
// phase 4. The pure-Go driver is a different SQLite build from the C one, and
// discovering it lacks FTS5 after the search feature is written would be an
// expensive surprise.
func TestFullTextSearchWorks(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})
	ctx := t.Context()

	rows := []struct{ path, name string }{
		{"/jeux/Cyberpunk 2077.iso", "Cyberpunk 2077.iso"},
		{"/documents/résumé.pdf", "résumé.pdf"},
		{"/jeux/Half-Life.iso", "Half-Life.iso"},
	}
	for _, r := range rows {
		if _, err := d.Write.ExecContext(ctx,
			`INSERT INTO file_index (path, name) VALUES (?, ?)`, r.path, r.name); err != nil {
			t.Fatalf("index %q: %v", r.name, err)
		}
	}

	var got string
	if err := d.Read.QueryRowContext(ctx,
		`SELECT name FROM file_index WHERE file_index MATCH 'cyberpunk'`).Scan(&got); err != nil {
		t.Fatalf("match: %v", err)
	}
	if got != "Cyberpunk 2077.iso" {
		t.Errorf("matched %q, want the Cyberpunk entry", got)
	}

	// remove_diacritics is configured on the tokeniser, so an unaccented query
	// finds an accented name. Typing accents on a phone keyboard is enough
	// friction that search would feel broken without this.
	if err := d.Read.QueryRowContext(ctx,
		`SELECT name FROM file_index WHERE file_index MATCH 'resume'`).Scan(&got); err != nil {
		t.Fatalf("accent-insensitive match: %v", err)
	}
	if got != "résumé.pdf" {
		t.Errorf("matched %q, want the accented entry", got)
	}
}

func TestRefusesToLiveInsideStorageRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inside := filepath.Join(root, "config")

	_, err := Open(t.Context(), Config{Dir: inside, StorageRoot: root})
	if !errors.Is(err, ErrDatabaseInStorageRoot) {
		t.Fatalf("Open = %v, want ErrDatabaseInStorageRoot", err)
	}

	// The refusal must come before anything is written, or a failed start
	// would still leave a database inside the browsable tree.
	if _, statErr := os.Stat(filepath.Join(inside, FileName)); !os.IsNotExist(statErr) {
		t.Error("a database file was created despite the refusal")
	}
}

func TestAcceptsConfigDirOutsideStorageRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "data")
	config := filepath.Join(base, "config")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	d := open(t, Config{Dir: config, StorageRoot: root})
	if err := d.Verify(t.Context()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestConcurrentReadsDuringWrite(t *testing.T) {
	t.Parallel()

	d := open(t, Config{})
	ctx := t.Context()

	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, updated_at)
		 VALUES ('pending', 'x', 1, 1)`); err != nil {
		t.Fatalf("insert inside the transaction: %v", err)
	}

	// The whole reason for two pools: a reader must not block behind an open
	// write transaction. A single pool capped at one connection would deadlock
	// here rather than return.
	var count int
	if err := d.Read.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("read during an open write transaction: %v", err)
	}
	if count != 0 {
		t.Errorf("reader saw %d uncommitted rows, want 0", count)
	}
}
