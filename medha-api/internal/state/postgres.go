package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Store wraps the *sql.DB plus a small amount of bookkeeping (the connection
// string, the migration version reached) so callers can introspect at runtime.
type Store struct {
	DB             *sql.DB
	ConnString     string
	SchemaVersion  int
}

// Options configures Open. Zero values are sensible defaults.
type Options struct {
	Host            string        // PostgreSQL host (required)
	Port            int           // PostgreSQL port (default: 5432)
	User            string        // PostgreSQL user (required)
	Password        string        // PostgreSQL password
	Database        string        // Database name (required)
	SSLMode         string        // SSL mode (default: disable)
	MaxOpenConns    int           // Max concurrent connections (default: 25)
	MaxIdleConns    int           // Max idle connections (default: 5)
	ConnMaxLifetime time.Duration // Connection max lifetime (default: 5 min)
}

// Open creates a connection pool to PostgreSQL, applies migrations, and returns
// a Store ready for use.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Host == "" {
		return nil, errors.New("state.Open: Host is required")
	}
	if opts.User == "" {
		return nil, errors.New("state.Open: User is required")
	}
	if opts.Database == "" {
		return nil, errors.New("state.Open: Database is required")
	}

	if opts.Port == 0 {
		opts.Port = 5432
	}
	if opts.MaxOpenConns == 0 {
		opts.MaxOpenConns = 25
	}
	if opts.MaxIdleConns == 0 {
		opts.MaxIdleConns = 5
	}
	if opts.ConnMaxLifetime == 0 {
		opts.ConnMaxLifetime = 5 * time.Minute
	}
	if opts.SSLMode == "" {
		opts.SSLMode = "disable"
	}

	// Build PostgreSQL connection string.
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		opts.Host, opts.Port, opts.User, opts.Password, opts.Database, opts.SSLMode)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("state.Open: sql.Open: %w", err)
	}

	db.SetMaxOpenConns(opts.MaxOpenConns)
	db.SetMaxIdleConns(opts.MaxIdleConns)
	db.SetConnMaxLifetime(opts.ConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state.Open: ping: %w", err)
	}

	version, err := Migrate(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("state.Open: migrate: %w", err)
	}

	return &Store{DB: db, ConnString: connStr, SchemaVersion: version}, nil
}

// Close releases the underlying DB.
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}
