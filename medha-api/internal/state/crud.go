package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SessionRow is the minimal session shape exposed by the state layer. The
// richer domain model lives in Task 7's models package — this struct is
// scoped to what capture (Task 8) needs.
type SessionRow struct {
	ID               string
	Project          string
	CWD              string
	Status           string
	ObservationCount int
	StartedAt        time.Time
	UpdatedAt        time.Time
	EndedAt          *time.Time
}

// EnsureSession inserts a session row if missing, otherwise no-ops. Returns
// the canonical row. Used on SessionStart (FR-1) and as a safety net before
// observation persistence.
func (s *Store) EnsureSession(ctx context.Context, id, project, cwd string) (*SessionRow, error) {
	if id == "" {
		return nil, errors.New("EnsureSession: id required")
	}
	now := time.Now().UTC()
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO sessions (id, project, cwd, status, started_at, updated_at)
        VALUES ($1, $2, $3, 'active', $4, $5)
        ON CONFLICT(id) DO NOTHING
    `, id, project, cwd, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("EnsureSession: %w", err)
	}
	return s.GetSession(ctx, id)
}

// GetSession reads a single session by ID; returns ErrNotFound if missing.
func (s *Store) GetSession(ctx context.Context, id string) (*SessionRow, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, project, cwd, status, observation_count, started_at, updated_at, ended_at
        FROM sessions WHERE id = $1
    `, id)
	var (
		r            SessionRow
		started, upd string
		ended        sql.NullString
		cwd          sql.NullString
	)
	if err := row.Scan(&r.ID, &r.Project, &cwd, &r.Status, &r.ObservationCount, &started, &upd, &ended); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if cwd.Valid {
		r.CWD = cwd.String
	}
	r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, upd)
	if ended.Valid {
		t, _ := time.Parse(time.RFC3339Nano, ended.String)
		r.EndedAt = &t
	}
	return &r, nil
}

// IncrementSessionObservationCount bumps the counter and updates updated_at.
func (s *Store) IncrementSessionObservationCount(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `
        UPDATE sessions
        SET observation_count = observation_count + 1, updated_at = $1
        WHERE id = $2
    `, time.Now().UTC().Format(time.RFC3339Nano), sessionID)
	return err
}

// MarkSessionEnded flips status to 'completed' and stamps ended_at. Idempotent.
func (s *Store) MarkSessionEnded(ctx context.Context, sessionID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
        UPDATE sessions
        SET status = 'completed', ended_at = COALESCE(ended_at, $1), updated_at = $2
        WHERE id = $3
    `, now, now, sessionID)
	return err
}

// ListProjects returns all distinct non-empty project names from sessions.
func (s *Store) ListProjects(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT project FROM sessions WHERE project != '' ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// ListSessions returns sessions ordered by started_at DESC, optionally filtered
// by project. Use an empty project string to list across all projects.
func (s *Store) ListSessions(ctx context.Context, project string, limit int) ([]*SessionRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, project, cwd, status, observation_count, started_at, updated_at, COALESCE(ended_at,'')
        FROM sessions
        WHERE ($1 = '' OR project = $1)
        ORDER BY started_at DESC LIMIT $2
    `, project, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*SessionRow
	for rows.Next() {
		var r SessionRow
		var started, upd, ended string
		var cwd sql.NullString
		if err := rows.Scan(&r.ID, &r.Project, &cwd, &r.Status, &r.ObservationCount, &started, &upd, &ended); err != nil {
			return nil, err
		}
		if cwd.Valid {
			r.CWD = cwd.String
		}
		r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, upd)
		if ended != "" {
			t, _ := time.Parse(time.RFC3339Nano, ended)
			r.EndedAt = &t
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// SystemCounts holds aggregate row counts for the stats endpoint.
type SystemCounts struct {
	Sessions     int
	Observations int
	Memories     int
}

// SystemStats returns aggregate counts. Individual query errors are silenced;
// counts default to zero rather than aborting the stats response.
func (s *Store) SystemStats(ctx context.Context) SystemCounts {
	var c SystemCounts
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&c.Sessions)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).Scan(&c.Observations)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&c.Memories)
	return c
}

// ListObservations returns recent observations ordered newest-first.
// Pass an empty sessionID to list across all sessions.
func (s *Store) ListObservations(ctx context.Context, sessionID string, limit int) ([]*ObservationRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, session_id, project, hook_type,
               tool_name, COALESCE(type,''), COALESCE(title,''), created_at
        FROM observations
        WHERE ($1 = '' OR session_id = $1)
        ORDER BY created_at DESC LIMIT $2
    `, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*ObservationRow
	for rows.Next() {
		var o ObservationRow
		var toolName sql.NullString
		var created string
		if err := rows.Scan(&o.ID, &o.SessionID, &o.Project, &o.HookType, &toolName, &o.Type, &o.Title, &created); err != nil {
			return nil, err
		}
		o.ToolName = toolName.String
		o.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, &o)
	}
	return out, rows.Err()
}

// ObservationRow is the storage-level view of an observation row. Higher
// layers convert this to/from the models.RawObservation / CompressedObservation
// shapes (Task 7).
type ObservationRow struct {
	ID            string
	SessionID     string
	Project       string
	HookType      string
	ToolName      string
	ToolInputJSON string
	ToolOutput    string
	UserPrompt    string
	RawJSON       string
	Modality      string
	ImageRef      string
	HasSecrets    bool
	Compressed    bool
	CreatedAt     time.Time

	// Populated only when compressed=1
	Type             string
	Title            string
	Subtitle         string
	FactsJSON        string
	Narrative        string
	ConceptsJSON     string
	FilesJSON        string
	Importance       int
	Confidence       float64
	ImageDescription string
	CompressedAt     *time.Time
}

// InsertRawObservation persists the raw (pre-compression) form of an
// observation. Called by Task 8 after the privacy filter has run.
func (s *Store) InsertRawObservation(ctx context.Context, o *ObservationRow) error {
	if o.ID == "" || o.SessionID == "" || o.HookType == "" {
		return errors.New("InsertRawObservation: id, session_id, hook_type required")
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO observations (
            id, session_id, project, hook_type,
            tool_name, tool_input_json, tool_output, user_prompt,
            raw_json, modality, image_ref, has_secrets,
            compressed, created_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 0, $13)
    `,
		o.ID, o.SessionID, o.Project, o.HookType,
		o.ToolName, o.ToolInputJSON, o.ToolOutput, o.UserPrompt,
		o.RawJSON, o.Modality, o.ImageRef, boolToInt(o.HasSecrets),
		o.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetObservation fetches a row by id. Returns ErrNotFound if missing.
func (s *Store) GetObservation(ctx context.Context, id string) (*ObservationRow, error) {
	row := s.DB.QueryRowContext(ctx, `
        SELECT id, session_id, project, hook_type,
               tool_name, tool_input_json, tool_output, user_prompt,
               raw_json, modality, image_ref, has_secrets, compressed,
               type, title, subtitle, facts_json, narrative, concepts_json,
               files_json, importance, confidence, image_description,
               created_at, compressed_at
        FROM observations WHERE id = $1
    `, id)

	var (
		o                                                ObservationRow
		toolName, toolInput, toolOutput, userPrompt      sql.NullString
		imageRef, typ, title, subtitle, facts, narrative sql.NullString
		concepts, files, imageDesc, compressedAt         sql.NullString
		importance                                       sql.NullInt64
		confidence                                       sql.NullFloat64
		hasSecrets, compressed                           int
		createdAt                                        string
	)
	if err := row.Scan(
		&o.ID, &o.SessionID, &o.Project, &o.HookType,
		&toolName, &toolInput, &toolOutput, &userPrompt,
		&o.RawJSON, &o.Modality, &imageRef, &hasSecrets, &compressed,
		&typ, &title, &subtitle, &facts, &narrative, &concepts,
		&files, &importance, &confidence, &imageDesc,
		&createdAt, &compressedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	o.ToolName, o.ToolInputJSON, o.ToolOutput, o.UserPrompt = toolName.String, toolInput.String, toolOutput.String, userPrompt.String
	o.ImageRef = imageRef.String
	o.HasSecrets = hasSecrets != 0
	o.Compressed = compressed != 0
	o.Type, o.Title, o.Subtitle = typ.String, title.String, subtitle.String
	o.FactsJSON, o.Narrative, o.ConceptsJSON, o.FilesJSON = facts.String, narrative.String, concepts.String, files.String
	o.ImageDescription = imageDesc.String
	if importance.Valid {
		o.Importance = int(importance.Int64)
	}
	if confidence.Valid {
		o.Confidence = confidence.Float64
	}
	o.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if compressedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, compressedAt.String)
		o.CompressedAt = &t
	}
	return &o, nil
}

// UpdateCompressedFields replaces the compressed-side columns on an existing
// observation row (called by Task 13's compressed callback).
func (s *Store) UpdateCompressedFields(ctx context.Context, id string, c *ObservationRow) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
        UPDATE observations SET
            compressed         = 1,
            type               = $1,
            title              = $2,
            subtitle           = $3,
            facts_json         = $4,
            narrative          = $5,
            concepts_json      = $6,
            files_json         = $7,
            importance         = $8,
            confidence         = $9,
            image_description  = $10,
            compressed_at      = $11
        WHERE id = $12
    `,
		c.Type, c.Title, c.Subtitle, c.FactsJSON, c.Narrative,
		c.ConceptsJSON, c.FilesJSON, c.Importance, c.Confidence,
		c.ImageDescription, now, id,
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
