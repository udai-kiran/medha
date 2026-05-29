package state

import (
	"context"
	"time"
)

// ShareMemory adds a memory to a team's shared feed (G22).
func (s *Store) ShareMemory(ctx context.Context, memoryID, teamID, sharedBy, mode string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if mode == "" {
		mode = "read"
	}
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO team_shared (memory_id, team_id, shared_by, mode, shared_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT(memory_id, team_id) DO UPDATE SET mode = excluded.mode, shared_by = excluded.shared_by
    `, memoryID, teamID, sharedBy, mode, now)
	if err != nil {
		return err
	}
	feedID := newID("feed")
	_, err = s.DB.ExecContext(ctx, `
        INSERT INTO team_feed (id, team_id, memory_id, shared_by, shared_at)
        VALUES ($1, $2, $3, $4, $5)
    `, feedID, teamID, memoryID, sharedBy, now)
	return err
}

// GetTeamFeed returns recent shared items for a team.
func (s *Store) GetTeamFeed(ctx context.Context, teamID string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `
        SELECT f.id, f.memory_id, f.shared_by, f.shared_at,
               COALESCE(m.title,''), COALESCE(m.content,'')
        FROM team_feed f
        LEFT JOIN memories m ON m.id = f.memory_id
        WHERE f.team_id = $1
        ORDER BY f.shared_at DESC LIMIT $2
    `, teamID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var id, memID, sharedBy, sharedAt, title, content string
		if rows.Scan(&id, &memID, &sharedBy, &sharedAt, &title, &content) == nil {
			out = append(out, map[string]any{
				"id": id, "memoryId": memID, "sharedBy": sharedBy,
				"sharedAt": sharedAt, "title": title, "content": content,
			})
		}
	}
	return out, rows.Err()
}

// SetSlot upserts a named memory slot (G24).
func (s *Store) SetSlot(ctx context.Context, project, slotName, content string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := newID("slot")
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO slots (id, project, slot_name, content, updated_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT(project, slot_name) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at
    `, id, project, slotName, content, now)
	return err
}

// GetSlot retrieves a named slot's content.
func (s *Store) GetSlot(ctx context.Context, project, slotName string) (string, error) {
	var content string
	err := s.DB.QueryRowContext(ctx,
		`SELECT content FROM slots WHERE ($1 = '' OR project = $2) AND slot_name = $3`,
		project, project, slotName,
	).Scan(&content)
	if err != nil {
		return "", ErrNotFound
	}
	return content, nil
}

// ListSlots returns all slots for a project.
func (s *Store) ListSlots(ctx context.Context, project string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT slot_name, content, updated_at FROM slots WHERE ($1 = '' OR project = $2) ORDER BY slot_name`,
		project, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var name, content, updated string
		if rows.Scan(&name, &content, &updated) == nil {
			out = append(out, map[string]any{"slotName": name, "content": content, "updatedAt": updated})
		}
	}
	return out, rows.Err()
}

// WorkingPush pushes a context item onto the working memory stack (G25).
func (s *Store) WorkingPush(ctx context.Context, sessionID, content string) (string, error) {
	id := newID("wm")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO working_memory (id, session_id, content, pushed_at) VALUES ($1, $2, $3, $4)`,
		id, sessionID, content, now)
	return id, err
}

// WorkingPop pops the top n items from the working memory stack.
func (s *Store) WorkingPop(ctx context.Context, sessionID string, n int) ([]string, error) {
	if n <= 0 {
		n = 1
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, content FROM working_memory WHERE session_id = $1 ORDER BY pushed_at DESC LIMIT $2`,
		sessionID, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	var out []string
	for rows.Next() {
		var id, content string
		if rows.Scan(&id, &content) == nil {
			ids = append(ids, id)
			out = append(out, content)
		}
	}
	for _, id := range ids {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM working_memory WHERE id = $1`, id)
	}
	return out, nil
}

// WorkingClear empties the working memory stack for a session.
func (s *Store) WorkingClear(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM working_memory WHERE session_id = $1`, sessionID)
	return err
}
