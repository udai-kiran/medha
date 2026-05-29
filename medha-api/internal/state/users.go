package state

import (
	"context"
	"encoding/json"
	"time"
)

// UserRow is a user record for multi-tenant support (G15).
type UserRow struct {
	ID          string
	Identifier  string
	DisplayName string
	Metadata    map[string]any
	CreatedAt   string
}

// EnsureUser creates or returns the user for the given identifier.
func (s *Store) EnsureUser(ctx context.Context, identifier, displayName string) (*UserRow, error) {
	// Try existing.
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, identifier, COALESCE(display_name,''), metadata_json, created_at
         FROM users WHERE identifier = $1`, identifier)
	var u UserRow
	var metaRaw string
	if err := row.Scan(&u.ID, &u.Identifier, &u.DisplayName, &metaRaw, &u.CreatedAt); err == nil {
		_ = json.Unmarshal([]byte(metaRaw), &u.Metadata)
		return &u, nil
	}
	// Create.
	u.ID = newID("usr")
	u.Identifier = identifier
	u.DisplayName = displayName
	u.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	metaJSON := "{}"
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO users (id, identifier, display_name, metadata_json, created_at)
         VALUES ($1, $2, $3, $4, $5)`,
		u.ID, u.Identifier, u.DisplayName, metaJSON, u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUser returns a user by identifier.
func (s *Store) GetUser(ctx context.Context, identifier string) (*UserRow, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, identifier, COALESCE(display_name,''), metadata_json, created_at
         FROM users WHERE identifier = $1`, identifier)
	var u UserRow
	var metaRaw string
	if err := row.Scan(&u.ID, &u.Identifier, &u.DisplayName, &metaRaw, &u.CreatedAt); err != nil {
		return nil, ErrNotFound
	}
	_ = json.Unmarshal([]byte(metaRaw), &u.Metadata)
	return &u, nil
}
