package state

import (
	"context"
	"time"
)

// FactRow is a subject–predicate–object triple.
type FactRow struct {
	ID         string
	Project    string
	Subject    string
	Predicate  string
	ObjectVal  string
	Confidence float64
	CreatedAt  string
}

// AddFact inserts a new fact triple.
func (s *Store) AddFact(ctx context.Context, project, subject, predicate, objectVal string, confidence float64) (*FactRow, error) {
	id := newID("fact")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if confidence <= 0 {
		confidence = 1.0
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO facts (id, project, subject, predicate, object_val, confidence, created_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, project, subject, predicate, objectVal, confidence, now)
	if err != nil {
		return nil, err
	}
	return &FactRow{
		ID: id, Project: project, Subject: subject, Predicate: predicate,
		ObjectVal: objectVal, Confidence: confidence, CreatedAt: now,
	}, nil
}

// SearchFacts returns facts matching subject, predicate, and/or a text query.
func (s *Store) SearchFacts(ctx context.Context, project, subject, predicate, query string, limit int) ([]*FactRow, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, project, subject, predicate, object_val, confidence, created_at
        FROM facts
        WHERE ($1 = '' OR project = $2)
        AND ($3 = '' OR subject = $3)
        AND ($4 = '' OR predicate = $4)
        AND ($5 = '%%' OR LOWER(subject || ' ' || predicate || ' ' || object_val) LIKE LOWER($5))
        ORDER BY confidence DESC, created_at DESC LIMIT $6
    `, project, project, subject, predicate, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*FactRow
	for rows.Next() {
		f := &FactRow{}
		if err := rows.Scan(&f.ID, &f.Project, &f.Subject, &f.Predicate,
			&f.ObjectVal, &f.Confidence, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
