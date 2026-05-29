package state

import (
	"context"
	"encoding/json"
	"time"
)

// PreferenceRow is a user preference record.
type PreferenceRow struct {
	ID           string
	Project      string
	Category     string
	Preference   string
	Confidence   float64
	Metadata     map[string]any
	CreatedAt    string
	UpdatedAt    string
	SupersededBy string
}

// AddPreference inserts a new preference.
func (s *Store) AddPreference(ctx context.Context, project, category, preference string, confidence float64, metadata map[string]any) (*PreferenceRow, error) {
	id := newID("pref")
	metaJSON, _ := json.Marshal(metadata)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if confidence <= 0 {
		confidence = 1.0
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO preferences (id, project, category, preference, confidence, metadata_json, created_at, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, project, category, preference, confidence, string(metaJSON), now)
	if err != nil {
		return nil, err
	}
	return &PreferenceRow{
		ID: id, Project: project, Category: category, Preference: preference,
		Confidence: confidence, Metadata: metadata, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// SearchPreferences searches preferences by category and/or content.
func (s *Store) SearchPreferences(ctx context.Context, project, category, query string, limit int) ([]*PreferenceRow, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, project, category, preference, confidence, metadata_json, created_at, updated_at, COALESCE(superseded_by,'')
        FROM preferences
        WHERE ($1 = '' OR project = $2)
        AND ($3 = '' OR category = $3)
        AND ($4 = '%%' OR LOWER(preference) LIKE LOWER($4))
        AND superseded_by IS NULL
        ORDER BY confidence DESC, created_at DESC LIMIT $5
    `, project, project, category, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPreferences(rows)
}

// DeletePreference removes a preference by id.
func (s *Store) DeletePreference(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM preferences WHERE id = $1`, id)
	return err
}

func scanPreferences(rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}) ([]*PreferenceRow, error) {
	defer func() { _ = rows.Close() }()
	var out []*PreferenceRow
	for rows.Next() {
		p := &PreferenceRow{}
		var metaRaw string
		if err := rows.Scan(&p.ID, &p.Project, &p.Category, &p.Preference,
			&p.Confidence, &metaRaw, &p.CreatedAt, &p.UpdatedAt, &p.SupersededBy); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metaRaw), &p.Metadata)
		out = append(out, p)
	}
	return out, rows.Err()
}
