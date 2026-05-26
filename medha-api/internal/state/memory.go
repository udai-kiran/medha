package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Memory tier constants. Mirrors models.MemoryTier but lives in state to
// avoid an import cycle from migration tooling.
const (
	TierWorking    = "working"
	TierEpisodic   = "episodic"
	TierSemantic   = "semantic"
	TierProcedural = "procedural"
)

// MemoryRow is the storage shape used by the consolidation pipeline (Task 22)
// and the decay job (Task 24). Field naming mirrors the schema columns.
type MemoryRow struct {
	ID                   string
	Project              string
	Type                 string
	Tier                 string
	Title                string
	Content              string
	Concepts             []string
	Files                []string
	SessionIDs           []string
	SourceObservationIDs []string
	Strength             float64
	IsLatest             bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	LastRetrievedAt      *time.Time
}

// InsertMemory stores a new memory row. Strength defaults to 1.0 if zero;
// tier defaults to "semantic".
func (s *Store) InsertMemory(ctx context.Context, m *MemoryRow) error {
	if m == nil || m.ID == "" || m.Title == "" {
		return errors.New("InsertMemory: id and title required")
	}
	if m.Tier == "" {
		m.Tier = TierSemantic
	}
	if m.Strength == 0 {
		m.Strength = 1.0
	}
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now

	concepts, _ := json.Marshal(m.Concepts)
	files, _ := json.Marshal(m.Files)
	sessions, _ := json.Marshal(m.SessionIDs)
	sources, _ := json.Marshal(m.SourceObservationIDs)

	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO memories (
            id, project, type, tier, title, content,
            concepts_json, files_json, session_ids_json, source_observation_ids,
            strength, is_latest, created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
    `, m.ID, m.Project, m.Type, m.Tier, m.Title, m.Content,
		string(concepts), string(files), string(sessions), string(sources),
		m.Strength, m.CreatedAt.Format(time.RFC3339Nano), m.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

// GetMemory fetches a memory by id. Returns ErrNotFound if absent.
func (s *Store) GetMemory(ctx context.Context, id string) (*MemoryRow, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, project, type, tier, title, content,
               concepts_json, files_json, session_ids_json, source_observation_ids,
               strength, is_latest, created_at, updated_at, last_retrieved_at
        FROM memories WHERE id = ?
    `, id)
	return scanMemory(row.Scan)
}

// ListMemoriesByTier returns memories for a project filtered by tier
// (empty tier returns all). Results ordered by strength desc, then created_at desc.
func (s *Store) ListMemoriesByTier(ctx context.Context, project, tier string, limit int) ([]*MemoryRow, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, project, type, tier, title, content,
                 concepts_json, files_json, session_ids_json, source_observation_ids,
                 strength, is_latest, created_at, updated_at, last_retrieved_at
          FROM memories
          WHERE (? = '' OR project = ?)
          AND (? = '' OR tier = ?)
          ORDER BY strength DESC, created_at DESC
          LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, q, project, project, tier, tier, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*MemoryRow
	for rows.Next() {
		m, err := scanMemory(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanMemory abstracts over *sql.Row / *sql.Rows since Scan signatures match.
func scanMemory(scan func(dest ...any) error) (*MemoryRow, error) {
	var (
		m                          MemoryRow
		concepts, files            sql.NullString
		sessions, sources          sql.NullString
		createdAt, updatedAt       string
		lastRetrievedAt            sql.NullString
		isLatestInt                int
	)
	err := scan(&m.ID, &m.Project, &m.Type, &m.Tier, &m.Title, &m.Content,
		&concepts, &files, &sessions, &sources,
		&m.Strength, &isLatestInt, &createdAt, &updatedAt, &lastRetrievedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(concepts.String), &m.Concepts)
	_ = json.Unmarshal([]byte(files.String), &m.Files)
	_ = json.Unmarshal([]byte(sessions.String), &m.SessionIDs)
	_ = json.Unmarshal([]byte(sources.String), &m.SourceObservationIDs)
	m.IsLatest = isLatestInt != 0
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if lastRetrievedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastRetrievedAt.String)
		m.LastRetrievedAt = &t
	}
	return &m, nil
}

// MarkRetrieved bumps last_retrieved_at; Task 24 uses it for reinforcement.
func (s *Store) MarkRetrieved(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano))
	for _, id := range ids {
		args = append(args, id)
	}
	q := fmt.Sprintf(`UPDATE memories SET last_retrieved_at = ? WHERE id IN (%s)`, placeholders)
	_, err := s.DB.ExecContext(ctx, q, args...)
	return err
}

// UpdateMemoryStrength sets a new strength value. Used by Task 24's decay job.
func (s *Store) UpdateMemoryStrength(ctx context.Context, id string, strength float64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE memories SET strength = ?, updated_at = ? WHERE id = ?`,
		strength, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// DeleteMemory hard-removes a memory row. Used by Task 24 for hard eviction.
func (s *Store) DeleteMemory(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	return err
}

// EvictExpiredObservations enforces tier TTLs on the observations table:
//   - Working tier (uncompressed raw observations): 24h hard delete.
//   - Episodic tier (compressed but unconsolidated): 7d hard delete.
// Returns counts evicted per tier.
//
// "Working" here = uncompressed; "Episodic" = compressed but session not ended.
// This is a heuristic mapping until Task 24 adds richer tier classification.
func (s *Store) EvictExpiredObservations(ctx context.Context, workingTTL, episodicTTL time.Duration) (working, episodic int64, err error) {
	now := time.Now().UTC()
	workingCutoff := now.Add(-workingTTL).Format(time.RFC3339Nano)
	episodicCutoff := now.Add(-episodicTTL).Format(time.RFC3339Nano)

	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM observations WHERE compressed = 0 AND created_at < ?`, workingCutoff)
	if err != nil {
		return 0, 0, err
	}
	working, _ = res.RowsAffected()

	res, err = s.DB.ExecContext(ctx, `
        DELETE FROM observations
        WHERE compressed = 1 AND created_at < ?
        AND session_id IN (SELECT id FROM sessions WHERE status != 'completed')
    `, episodicCutoff)
	if err != nil {
		return working, 0, err
	}
	episodic, _ = res.RowsAffected()
	return working, episodic, nil
}
