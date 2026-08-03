package db

import (
	"os"
	"path/filepath"
	"testing"
)

func seedUser(t *testing.T, d *DB, name string) {
	t.Helper()
	_, err := d.Write.ExecContext(t.Context(),
		`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, 'x', 0, 0)`, name)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	d := open(t, Config{Dir: dir})
	seedUser(t, d, "alice")

	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := BackupTo(t.Context(), DBPath(dir), dest); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if err := validateBackup(t.Context(), dest); err != nil {
		t.Fatalf("the snapshot is not a valid backup: %v", err)
	}

	// Restore into an empty directory and confirm the account survived.
	restoreDir := t.TempDir()
	report, err := RestoreFrom(t.Context(), restoreDir, dest)
	if err != nil {
		t.Fatalf("RestoreFrom: %v", err)
	}
	if report.PreviousSaved != "" {
		t.Errorf("PreviousSaved = %q, want empty for a fresh directory", report.PreviousSaved)
	}

	restored := open(t, Config{Dir: restoreDir})
	var name string
	if err := restored.Read.QueryRowContext(t.Context(),
		`SELECT username FROM users LIMIT 1`).Scan(&name); err != nil {
		t.Fatalf("read restored database: %v", err)
	}
	if name != "alice" {
		t.Errorf("restored user = %q, want alice", name)
	}
}

func TestBackupRefusesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	open(t, Config{Dir: dir})

	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := BackupTo(t.Context(), DBPath(dir), dest); err == nil {
		t.Fatal("expected BackupTo to refuse an existing target")
	}
}

func TestRestoreRejectsNonZefileDatabase(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "bogus.db")
	if err := os.WriteFile(bogus, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreFrom(t.Context(), t.TempDir(), bogus); err == nil {
		t.Fatal("expected RestoreFrom to reject a file that is not a Zefile database")
	}
}

func TestRestoreSavesTheReplacedDatabase(t *testing.T) {
	// A valid snapshot to restore from.
	srcDir := t.TempDir()
	src := open(t, Config{Dir: srcDir})
	seedUser(t, src, "bob")
	snap := filepath.Join(t.TempDir(), "snap.db")
	if err := BackupTo(t.Context(), DBPath(srcDir), snap); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	// A target directory that already holds a database. Close it first, as a
	// real restore is run with the server stopped.
	dstDir := t.TempDir()
	existing := open(t, Config{Dir: dstDir})
	if err := existing.Close(); err != nil {
		t.Fatalf("close existing: %v", err)
	}

	report, err := RestoreFrom(t.Context(), dstDir, snap)
	if err != nil {
		t.Fatalf("RestoreFrom: %v", err)
	}
	if report.PreviousSaved == "" {
		t.Fatal("expected the replaced database to be saved aside")
	}
	if _, err := os.Stat(report.PreviousSaved); err != nil {
		t.Errorf("saved copy missing: %v", err)
	}
}
