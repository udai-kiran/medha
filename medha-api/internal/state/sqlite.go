// Package state owns the SQLite-backed persistence layer: connection
// management, schema migrations, and a KV abstraction over the documented
// scopes. Higher layers (api, search, consolidation) talk to this package
// rather than touching SQL directly.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// modernc.org/sqlite is a pure-Go driver — no cgo, so the distroless
	// scratch image (Task 4) stays static.
	_ "modernc.org/sqlite"
)

// Store wraps the *sql.DB plus a small amount of bookkeeping (the open path,
// the migration version reached) so callers can introspect at runtime.
type Store struct {
	DB             *sql.DB
	Path           string
	SchemaVersion  int
}

// Options configures Open. Zero values are sensible defaults.
type Options struct {
	// Path is the file path; ":memory:" yields an in-memory DB (useful for tests).
	Path string
	// MaxOpenConns caps concurrent writers; SQLite serialises writes anyway, but
	// readers benefit from a small pool. Defaults to 4.
	MaxOpenConns int
	// BusyTimeout is the duration SQLite waits on a locked database before
	// returning SQLITE_BUSY. Defaults to 5s.
	BusyTimeout time.Duration
}

// Open creates (or opens) the SQLite file, applies pragmas (WAL, busy timeout,
// foreign keys), and runs migrations forward to the latest version.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Path == "" {
		return nil, errors.New("state.Open: Path is required")
	}
	if opts.MaxOpenConns == 0 {
		opts.MaxOpenConns = 4
	}
	if opts.BusyTimeout == 0 {
		opts.BusyTimeout = 5 * time.Second
	}

	// Ensure parent directory exists for file-backed DBs.
	if opts.Path != ":memory:" {
		if dir := filepath.Dir(opts.Path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("state.Open: mkdir %q: %w", dir, err)
			}
		}
	}

	// modernc.org/sqlite accepts query params on the DSN — set WAL + busy
	// timeout up front so the very first connection sees them.
	dsn := opts.Path
	if opts.Path != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(on)&_pragma=synchronous(NORMAL)",
			opts.Path, opts.BusyTimeout.Milliseconds())
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("state.Open: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(opts.MaxOpenConns)
	db.SetMaxIdleConns(opts.MaxOpenConns)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state.Open: ping: %w", err)
	}

	version, err := Migrate(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state.Open: migrate: %w", err)
	}

	return &Store{DB: db, Path: opts.Path, SchemaVersion: version}, nil
}

// Close releases the underlying DB.
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}
