package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	stmt := "VACUUM INTO '" + escapeSQLString(dest) + "'" // #nosec G201 G202 -- dest is single-quote-escaped; VACUUM INTO takes no bind parameter
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

	// Diverged lists paths the restored database still references but that no
	// longer exist on disk — the metadata got ahead of, or behind, the files.
	// Empty when no data directory was given to check against.
	Diverged []string
}

// RestoreFrom replaces the database in configDir with the backup at src.
//
// The server must be stopped first: replacing the file under a running instance
// would corrupt its open connection. src is validated as a healthy Zefile
// database before anything is touched, and the database being replaced is copied
// aside first, so a mistaken restore is itself reversible.
//
// When dataDir is given, the restored database is compared against the files on
// disk and any references to missing files are reported — restoring an old
// snapshot onto a newer tree (or the reverse) leaves the two out of step, and
// this says where.
func RestoreFrom(ctx context.Context, configDir, dataDir, src string) (RestoreReport, error) {
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

	// Best-effort: a failed scan must not fail an otherwise-good restore.
	if dataDir != "" {
		report.Diverged, _ = checkDivergence(ctx, dst, dataDir)
	}

	return report, nil
}

// checkDivergence returns the paths the database references — through access
// rules, ownership and shares — that no longer exist under dataDir. The reverse
// (files on disk the database does not mention) is normal for Zefile, where the
// filesystem is the authority, so it is not reported.
func checkDivergence(ctx context.Context, dbPath, dataDir string) ([]string, error) {
	pool, err := sql.Open("sqlite", dbPath+"?_pragma=query_only(true)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer func() { _ = pool.Close() }()

	rows, err := pool.QueryContext(ctx, `
		SELECT path FROM acl
		UNION SELECT path FROM file_owners
		UNION SELECT path FROM shares`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var missing []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		disk := filepath.Join(dataDir, filepath.FromSlash(strings.TrimPrefix(p, "/")))
		if _, err := os.Stat(disk); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, p)
		}
	}
	return missing, rows.Err()
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
	in, err := os.Open(src) // #nosec G304 -- src is an operator-supplied backup path, by design
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- dst is an operator-supplied path, by design
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
