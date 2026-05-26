package state

import (
	"context"
	"database/sql"
	"fmt"
)

// migration is a single forward-only schema step.
type migration struct {
	version int
	name    string
	sql     string
}

// Migrate runs every pending migration in a transaction and returns the
// schema version reached. Safe to call on every startup.
func Migrate(ctx context.Context, db *sql.DB) (int, error) {
	if _, err := db.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS schema_version (
            version    INTEGER PRIMARY KEY,
            name       TEXT NOT NULL,
            applied_at TEXT NOT NULL DEFAULT (datetime('now'))
        )
    `); err != nil {
		return 0, fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return current, fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		current = m.version
	}
	return current, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_version (version, name) VALUES (?, ?)`,
		m.version, m.name,
	); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit()
}
