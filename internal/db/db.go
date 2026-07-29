// Package db owns the SQLite connection and the schema.
//
// SQLite holds metadata only — accounts, permissions, shares, trash, ownership,
// the search index. It never holds file content, and it is never the authority
// on what exists: the filesystem is. A row describing a file that has since
// been deleted over SSH is stale data, not corruption.
//
// # Two pools
//
// SQLite in WAL mode permits many concurrent readers alongside a single writer.
// A writer pool capped at one connection turns a race for the write lock into
// an orderly queue, while readers stay parallel. One shared pool would either
// serialise reads needlessly or produce SQLITE_BUSY under load.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the binary stays static

	"github.com/krishna2206/zefile/internal/storage"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// FileName is the database file inside the configuration directory.
const FileName = "zefile.db"

// Config configures [Open].
type Config struct {
	// Dir is the configuration directory holding the database. Required.
	Dir string

	// StorageRoot, when set, is checked to make sure the database does not sit
	// inside the browsable tree. Leave empty only in tests.
	StorageRoot string

	// MaxReaders caps concurrent read connections. Zero picks a sensible
	// default.
	MaxReaders int
}

// DB is an open database with its two connection pools.
type DB struct {
	// Write serialises writes through a single connection.
	Write *sql.DB

	// Read serves concurrent reads.
	Read *sql.DB

	path string
}

// DefaultMaxReaders bounds the reader pool. Metadata queries are short, and
// more connections than this buys nothing while costing file descriptors.
const DefaultMaxReaders = 8

// ErrDatabaseInStorageRoot means the configuration directory lies inside the
// storage tree, which would let users list, download and delete the database
// through the file browser — password hashes included.
var ErrDatabaseInStorageRoot = errors.New("db: the configuration directory is inside the storage root")

// Open prepares the database: it verifies placement, applies migrations, and
// returns pools ready for use.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.Dir == "" {
		return nil, errors.New("db: configuration directory is required")
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("db: create %q: %w", cfg.Dir, err)
	}

	if cfg.StorageRoot != "" {
		inside, err := storage.ContainsPath(cfg.StorageRoot, cfg.Dir)
		if err != nil {
			return nil, fmt.Errorf("db: check placement: %w", err)
		}
		if inside {
			return nil, fmt.Errorf("%w: %q is inside %q", ErrDatabaseInStorageRoot, cfg.Dir, cfg.StorageRoot)
		}
	}

	path := filepath.Join(cfg.Dir, FileName)

	write, err := openPool(path, 1)
	if err != nil {
		return nil, err
	}

	if err := migrate(ctx, write); err != nil {
		_ = write.Close()
		return nil, err
	}

	readers := cfg.MaxReaders
	if readers <= 0 {
		readers = DefaultMaxReaders
	}
	read, err := openPool(path, readers)
	if err != nil {
		_ = write.Close()
		return nil, err
	}

	return &DB{Write: write, Read: read, path: path}, nil
}

// Path returns the database file location, for diagnostics and backups.
func (d *DB) Path() string { return d.path }

// Close shuts both pools down, reporting the first failure.
func (d *DB) Close() error {
	readErr := d.Read.Close()
	writeErr := d.Write.Close()
	return errors.Join(readErr, writeErr)
}

// openPool opens one pool with the pragmas Zefile depends on.
//
// The pragmas travel in the DSN rather than being issued after connecting: a
// pool opens connections lazily and reconnects on its own, so anything set by a
// one-off statement would apply to the first connection and silently not to the
// rest.
func openPool(path string, maxConns int) (*sql.DB, error) {
	dsn := path +
		// Concurrent readers alongside one writer, and the reason for two pools.
		"?_pragma=journal_mode(WAL)" +
		// Wait for a contended lock instead of failing immediately.
		"&_pragma=busy_timeout(5000)" +
		// Every ON DELETE clause in the schema is inert without this.
		"&_pragma=foreign_keys(ON)" +
		// With WAL, NORMAL survives process crashes; only a host power loss can
		// cost the most recent transactions. The trade is a large write speedup.
		"&_pragma=synchronous(NORMAL)"

	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", path, err)
	}
	pool.SetMaxOpenConns(maxConns)
	pool.SetMaxIdleConns(maxConns)
	pool.SetConnMaxLifetime(time.Hour)

	if err := pool.Ping(); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("db: connect to %q: %w", path, err)
	}
	return pool, nil
}

// migrate brings the schema up to date, on the writer pool so it cannot run
// concurrently with itself.
//
// goose's package-level API (SetDialect, SetBaseFS, SetLogger) writes to
// globals, so two databases opened at once would race on them — the detector
// caught exactly that. Production opens one database at startup and would never
// have noticed, which is what makes the provider API the right choice rather
// than an assumption about call sites.
func migrate(ctx context.Context, pool *sql.DB) error {
	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: locate migrations: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, migrations)
	if err != nil {
		return fmt.Errorf("db: migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	return nil
}

// Verify runs the integrity and foreign-key checks SQLite offers.
//
// It is not called on every start — a large database makes it slow — but it
// backs a maintenance command and gives the backup work in lot 5.1 something
// to assert against.
func (d *DB) Verify(ctx context.Context) error {
	var result string
	if err := d.Read.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("db: integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("db: integrity check failed: %s", result)
	}

	rows, err := d.Read.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("db: foreign key check: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		return errors.New("db: foreign key check found violations")
	}
	return rows.Err()
}
