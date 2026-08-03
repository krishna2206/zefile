package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The database is the irreplaceable half of an instance: the files live on disk
// and are backed up by other means, but accounts, permissions, shares and tokens
// live only here. These two functions back the `zefile backup` and
// `zefile restore` commands.

// DBPath returns the database file location inside a configuration directory.
func DBPath(dir string) string { return filepath.Join(dir, FileName) }

// BackupTo writes a consistent snapshot of the database to dest using SQLite's
// VACUUM INTO. It is safe to run while the server is live: VACUUM INTO reads a
// transactionally consistent view, so the copy is never torn, and it also
// defragments the result. The target must not already exist.
func BackupTo(ctx context.Context, dbPath, dest string) error {
	if dest == "" {
		return errors.New("db: backup destination is required")
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("db: backup target already exists: %s", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("db: create backup directory: %w", err)
	}

	pool, err := openPool(dbPath, 1)
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()

	// The path is a string literal in the statement rather than a bound
	// parameter, quoted and escaped, because VACUUM INTO takes a filename
	// expression that not every driver accepts as a placeholder.
	stmt := "VACUUM INTO '" + escapeSQLString(dest) + "'"
	if _, err := pool.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("db: vacuum into %q: %w", dest, err)
	}
	return nil
}

// RestoreReport describes what a restore did.
type RestoreReport struct {
	// PreviousSaved is where the database that was replaced got copied, or empty
	// if there was none.
	PreviousSaved string
}

// RestoreFrom replaces the database in configDir with the backup at src.
//
// The server must be stopped first: replacing the file under a running instance
// would corrupt its open connection. src is validated as a healthy Zefile
// database before anything is touched, and the database being replaced is copied
// aside first, so a mistaken restore is itself reversible.
func RestoreFrom(ctx context.Context, configDir, src string) (RestoreReport, error) {
	if err := validateBackup(ctx, src); err != nil {
		return RestoreReport{}, err
	}

	dst := DBPath(configDir)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return RestoreReport{}, fmt.Errorf("db: create configuration directory: %w", err)
	}

	var report RestoreReport
	if _, err := os.Stat(dst); err == nil {
		saved := dst + ".pre-restore-" + time.Now().Format("20060102-150405")
		if err := copyFile(dst, saved); err != nil {
			return RestoreReport{}, fmt.Errorf("db: save current database: %w", err)
		}
		report.PreviousSaved = saved
	}

	if err := copyFile(src, dst); err != nil {
		return RestoreReport{}, fmt.Errorf("db: write restored database: %w", err)
	}
	// A stale write-ahead log or shared-memory file from the old database would
	// be applied on top of the restored one. They belong to the file we just
	// replaced, so they are removed.
	_ = os.Remove(dst + "-wal")
	_ = os.Remove(dst + "-shm")

	return report, nil
}

// validateBackup refuses a file that is not a healthy Zefile database, so a
// restore fails loudly before overwriting anything rather than leaving a broken
// instance.
func validateBackup(ctx context.Context, src string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("db: read backup %q: %w", src, err)
	}

	// Read-only, so validating never mutates the backup (no WAL switch, no
	// journal). A separate open from the pools, on the file directly.
	pool, err := sql.Open("sqlite", src+"?_pragma=query_only(true)&_pragma=busy_timeout(2000)")
	if err != nil {
		return fmt.Errorf("db: open backup %q: %w", src, err)
	}
	defer func() { _ = pool.Close() }()

	var result string
	if err := pool.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("db: backup is not a readable SQLite database: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("db: backup failed its integrity check: %s", result)
	}

	// A plausible-but-wrong SQLite file (someone's other database) should be
	// rejected too: a Zefile backup has these tables.
	for _, table := range []string{"users", "goose_db_version"} {
		var n int
		q := "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?"
		if err := pool.QueryRowContext(ctx, q, table).Scan(&n); err != nil {
			return fmt.Errorf("db: inspect backup: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("db: %q does not look like a Zefile backup (missing table %q)", src, table)
		}
	}
	return nil
}

// escapeSQLString escapes a value for use inside single quotes in an SQL string
// literal, by doubling embedded single quotes.
func escapeSQLString(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
