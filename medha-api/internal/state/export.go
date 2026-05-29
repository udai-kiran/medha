package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExportBundle is the full serialisable dump of a project's data.
type ExportBundle struct {
	Version     int              `json:"version"`
	ExportedAt  string           `json:"exportedAt"`
	Project     string           `json:"project"`
	Sessions    []map[string]any `json:"sessions"`
	Observations []map[string]any `json:"observations"`
	Memories    []map[string]any `json:"memories"`
	Patterns    []map[string]any `json:"patterns"`
}

// Export serialises all data for a project into an ExportBundle.
func (s *Store) Export(ctx context.Context, project string) (*ExportBundle, error) {
	bundle := &ExportBundle{
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Project:    project,
	}

	// Sessions.
	srows, err := s.DB.QueryContext(ctx,
		`SELECT id, project, cwd, status, observation_count, tags_json,
                summary, started_at, updated_at, ended_at
         FROM sessions WHERE ($1 = '' OR project = $2) ORDER BY started_at`,
		project, project)
	if err != nil {
		return nil, fmt.Errorf("export sessions: %w", err)
	}
	defer func() { _ = srows.Close() }()
	for srows.Next() {
		var id, proj, cwd, status, tags, started, updated string
		var obsCount int
		var summary, ended *string
		if err := srows.Scan(&id, &proj, &cwd, &status, &obsCount, &tags, &summary, &started, &updated, &ended); err != nil {
			return nil, err
		}
		row := map[string]any{
			"id": id, "project": proj, "cwd": cwd, "status": status,
			"observationCount": obsCount, "tagsJson": tags,
			"startedAt": started, "updatedAt": updated,
		}
		if summary != nil {
			row["summary"] = *summary
		}
		if ended != nil {
			row["endedAt"] = *ended
		}
		bundle.Sessions = append(bundle.Sessions, row)
	}

	// Observations (compressed only).
	orows, err := s.DB.QueryContext(ctx,
		`SELECT id, session_id, project, hook_type, tool_name, type, title,
                subtitle, narrative, concepts_json, files_json, facts_json,
                modality, compressed, created_at
         FROM observations
         WHERE ($1 = '' OR project = $2) AND compressed = 1
         ORDER BY created_at`,
		project, project)
	if err != nil {
		return nil, fmt.Errorf("export observations: %w", err)
	}
	defer func() { _ = orows.Close() }()
	for orows.Next() {
		var id, sessID, proj, hook, modality, created string
		var toolName, typ, title, subtitle, narrative, concepts, files, facts *string
		var compressed int
		if err := orows.Scan(&id, &sessID, &proj, &hook, &toolName, &typ, &title,
			&subtitle, &narrative, &concepts, &files, &facts,
			&modality, &compressed, &created); err != nil {
			return nil, err
		}
		row := map[string]any{
			"id": id, "sessionId": sessID, "project": proj, "hookType": hook,
			"modality": modality, "compressed": compressed != 0, "createdAt": created,
		}
		strOrEmpty := func(p *string, key string) {
			if p != nil {
				row[key] = *p
			}
		}
		strOrEmpty(toolName, "toolName")
		strOrEmpty(typ, "type")
		strOrEmpty(title, "title")
		strOrEmpty(subtitle, "subtitle")
		strOrEmpty(narrative, "narrative")
		strOrEmpty(concepts, "conceptsJson")
		strOrEmpty(files, "filesJson")
		strOrEmpty(facts, "factsJson")
		bundle.Observations = append(bundle.Observations, row)
	}

	// Memories.
	mems, err := s.ListMemoriesByTier(ctx, project, "", 10000)
	if err != nil {
		return nil, fmt.Errorf("export memories: %w", err)
	}
	for _, m := range mems {
		cJSON, _ := json.Marshal(m.Concepts)
		fJSON, _ := json.Marshal(m.Files)
		bundle.Memories = append(bundle.Memories, map[string]any{
			"id": m.ID, "project": m.Project, "type": m.Type, "tier": m.Tier,
			"title": m.Title, "content": m.Content,
			"conceptsJson": string(cJSON), "filesJson": string(fJSON),
			"strength": m.Strength, "isLatest": m.IsLatest,
			"createdAt": m.CreatedAt.Format(time.RFC3339Nano),
		})
	}

	// Patterns.
	pats, err := s.ListPatterns(ctx, project, 500)
	if err == nil {
		for _, p := range pats {
			ex, _ := json.Marshal(p.Examples)
			bundle.Patterns = append(bundle.Patterns, map[string]any{
				"id": p.ID, "pattern": p.Pattern, "count": p.Count,
				"examples": string(ex), "lastSeen": p.LastSeen,
			})
		}
	}

	return bundle, nil
}

// Import restores sessions, memories and patterns from a bundle.
// Observations are skipped (too large; use the JSONL importer for those).
func (s *Store) Import(ctx context.Context, bundle *ExportBundle) (imported int, err error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, sess := range bundle.Sessions {
		id, _ := sess["id"].(string)
		proj, _ := sess["project"].(string)
		cwd, _ := sess["cwd"].(string)
		status, _ := sess["status"].(string)
		if status == "" {
			status = "completed"
		}
		started, _ := sess["startedAt"].(string)
		if started == "" {
			started = now
		}
		_, _ = s.DB.ExecContext(ctx, `
            INSERT INTO sessions (id, project, cwd, status, started_at, updated_at)
            VALUES ($1, $2, $3, $4, $5, $5)
            ON CONFLICT(id) DO NOTHING
        `, id, proj, cwd, status, started)
		imported++
	}

	for _, m := range bundle.Memories {
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		row := &MemoryRow{
			ID:      id,
			Project: stringOrEmpty(m["project"]),
			Type:    stringOrEmpty(m["type"]),
			Tier:    stringOrEmpty(m["tier"]),
			Title:   stringOrEmpty(m["title"]),
			Content: stringOrEmpty(m["content"]),
		}
		if row.Tier == "" {
			row.Tier = TierSemantic
		}
		if s, ok := m["strength"].(float64); ok {
			row.Strength = s
		}
		_ = json.Unmarshal([]byte(stringOrEmpty(m["conceptsJson"])), &row.Concepts)
		_ = json.Unmarshal([]byte(stringOrEmpty(m["filesJson"])), &row.Files)
		if err := s.InsertMemory(ctx, row); err == nil {
			imported++
		}
	}
	return imported, nil
}

// SnapshotRow stores a point-in-time export bundle.
type SnapshotRow struct {
	ID         string
	Project    string
	CreatedAt  string
	BundleJSON string
}

// CreateSnapshot exports and stores the current state as a named snapshot.
func (s *Store) CreateSnapshot(ctx context.Context, project string) (*SnapshotRow, error) {
	bundle, err := s.Export(ctx, project)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	id := "snap-" + uint32Hex(fnv32(project+time.Now().String()))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.DB.ExecContext(ctx, `
        INSERT INTO snapshots (id, project, bundle_json, created_at)
        VALUES ($1, $2, $3, $4)
    `, id, project, string(data), now)
	if err != nil {
		return nil, err
	}
	return &SnapshotRow{ID: id, Project: project, CreatedAt: now, BundleJSON: string(data)}, nil
}

// GetSnapshot retrieves a snapshot by id.
func (s *Store) GetSnapshot(ctx context.Context, id string) (*SnapshotRow, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, project, bundle_json, created_at FROM snapshots WHERE id = $1`, id)
	var snap SnapshotRow
	if err := row.Scan(&snap.ID, &snap.Project, &snap.BundleJSON, &snap.CreatedAt); err != nil {
		return nil, ErrNotFound
	}
	return &snap, nil
}

func stringOrEmpty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ImportJSONL imports Claude Code .jsonl transcript lines as raw observations.
// Each line is a JSON object; we look for tool_use and tool_result pairs.
func (s *Store) ImportJSONL(ctx context.Context, project, sessionID string, lines []string) (int, error) {
	if sessionID == "" {
		sessionID = "import-" + uint32Hex(fnv32(strings.Join(lines[:min(5, len(lines))], "")))
	}
	_, _ = s.EnsureSession(ctx, sessionID, project, "")

	imported := 0
	now := time.Now().UTC()
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		// Extract tool_use or tool_result entries.
		msgType, _ := obj["type"].(string)
		if msgType != "tool_use" && msgType != "tool_result" {
			continue
		}
		obsID := fmt.Sprintf("imp-%s-%d", sessionID[:min(8, len(sessionID))], i)
		rawJSON, _ := json.Marshal(obj)
		ts := now.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano)
		_, err := s.DB.ExecContext(ctx, `
            INSERT INTO observations
                (id, session_id, project, hook_type, raw_json, compressed, created_at)
            VALUES ($1, $2, $3, 'import', $4, 0, $5)
            ON CONFLICT(id) DO NOTHING
        `, obsID, sessionID, project, string(rawJSON), ts)
		if err == nil {
			imported++
		}
	}
	return imported, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
