package state

import (
	"context"
	"fmt"
	"strings"
)

// TimelineFilter controls what the Timeline query returns.
type TimelineFilter struct {
	Project  string
	Session  string
	HookType string
	FilePath string // filter to observations whose files_json contains this path
	After    string // ISO-8601 cursor — return entries with created_at > After
	Before   string // ISO-8601 cursor — return entries with created_at < Before
	Limit    int
}

// TimelineEntry is a slim observation row for the timeline view.
type TimelineEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Project   string `json:"project"`
	HookType  string `json:"hookType"`
	ToolName  string `json:"toolName"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Narrative string `json:"narrative"`
	Modality  string `json:"modality"`
	Compressed bool  `json:"compressed"`
	CreatedAt string `json:"createdAt"`
}

// Timeline returns observations matching the filter in chronological order.
func (s *Store) Timeline(ctx context.Context, f TimelineFilter) ([]*TimelineEntry, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}

	args := []any{}
	conds := []string{}
	idx := 1

	placeholder := func(v any) string {
		args = append(args, v)
		p := fmt.Sprintf("$%d", idx)
		idx++
		return p
	}

	if f.Project != "" {
		p := placeholder(f.Project)
		conds = append(conds, fmt.Sprintf("project = %s", p))
	}
	if f.Session != "" {
		p := placeholder(f.Session)
		conds = append(conds, fmt.Sprintf("session_id = %s", p))
	}
	if f.HookType != "" {
		p := placeholder(f.HookType)
		conds = append(conds, fmt.Sprintf("hook_type = %s", p))
	}
	if f.FilePath != "" {
		needle := `["` + f.FilePath + `"]`
		p := placeholder(needle)
		conds = append(conds, fmt.Sprintf("files_json::jsonb @> %s::jsonb", p))
	}
	if f.After != "" {
		p := placeholder(f.After)
		conds = append(conds, fmt.Sprintf("created_at > %s", p))
	}
	if f.Before != "" {
		p := placeholder(f.Before)
		conds = append(conds, fmt.Sprintf("created_at < %s", p))
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	limitP := placeholder(f.Limit)

	q := fmt.Sprintf(`
        SELECT id, session_id, COALESCE(project,''), hook_type,
               COALESCE(tool_name,''), COALESCE(type,''), COALESCE(title,''),
               COALESCE(narrative,''), COALESCE(modality,'text'), compressed, created_at
        FROM observations
        %s
        ORDER BY created_at ASC
        LIMIT %s
    `, where, limitP)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*TimelineEntry
	for rows.Next() {
		e := &TimelineEntry{}
		var compInt int
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Project, &e.HookType,
			&e.ToolName, &e.Type, &e.Title, &e.Narrative, &e.Modality,
			&compInt, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Compressed = compInt != 0
		out = append(out, e)
	}
	return out, rows.Err()
}
