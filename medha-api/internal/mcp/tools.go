package mcp

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"

	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
)

// cryptoRandRead is a one-line wrapper so the call site reads cleanly.
var cryptoRandRead = cryptorand.Read

// MemoryToolsDeps bundles the dependencies the agent_mem MCP tools need.
// Mirrors api.RouterDeps but narrow to what tools call directly.
type MemoryToolsDeps struct {
	Store  *state.Store
	Search *search.Hybrid
}

// RegisterMemoryTools wires the agent_mem-specific tools (recall, remember,
// session-history, status, smart-search). The set mirrors the four MCP
// "slash skills" called out in agent_mem.md §"MCP Server" plus a couple of
// utility tools that the REST API also exposes.
func RegisterMemoryTools(s *Server, deps MemoryToolsDeps) {
	s.RegisterTool(ToolDefinition{
		Name:        "smart-search",
		Description: "Hybrid search over compressed observations (BM25 + vector + graph fused via RRF).",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query":   map[string]any{"type": "string"},
				"project": map[string]any{"type": "string"},
				"mode":    map[string]any{"type": "string", "enum": []string{"bm25", "vector", "graph", "hybrid"}},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			var p struct {
				Query   string `json:"query"`
				Project string `json:"project"`
				Mode    string `json:"mode"`
				Limit   int    `json:"limit"`
			}
			if e := MustParseArgs(args, &p); e != nil {
				return nil, e
			}
			if p.Query == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "query is required"}
			}
			if p.Mode == "" {
				p.Mode = "hybrid"
			}
			if p.Limit <= 0 {
				p.Limit = 10
			}
			hits, err := deps.Search.Search(ctx, p.Project, p.Query, p.Mode, p.Limit)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"hits": hits, "mode": p.Mode}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "recall",
		Description: "Recall a memory by id, marking it retrieved (reinforces decay strength).",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{"memoryId"},
			"properties": map[string]any{"memoryId": map[string]any{"type": "string"}},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			id, _ := args["memoryId"].(string)
			if id == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "memoryId is required"}
			}
			m, err := deps.Store.GetMemory(ctx, id)
			if err == state.ErrNotFound {
				return nil, &Error{Code: ErrInvalidParams, Message: "memory not found"}
			}
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			_ = deps.Store.MarkRetrieved(ctx, []string{id})
			return m, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "remember",
		Description: "Persist a new memory (type, title, content, concepts, files, tier).",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{"type", "title"},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"type":     map[string]any{"type": "string"},
				"title":    map[string]any{"type": "string"},
				"content":  map[string]any{"type": "string"},
				"concepts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"files":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"tier":     map[string]any{"type": "string", "enum": []string{"working", "episodic", "semantic", "procedural"}},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			var p struct {
				Project  string   `json:"project"`
				Type     string   `json:"type"`
				Title    string   `json:"title"`
				Content  string   `json:"content"`
				Concepts []string `json:"concepts"`
				Files    []string `json:"files"`
				Tier     string   `json:"tier"`
			}
			if e := MustParseArgs(args, &p); e != nil {
				return nil, e
			}
			if p.Title == "" || p.Type == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "type and title are required"}
			}
			if p.Tier == "" {
				p.Tier = state.TierSemantic
			}
			id := newID("mem")
			row := &state.MemoryRow{
				ID: id, Project: p.Project, Type: p.Type, Tier: p.Tier,
				Title: p.Title, Content: p.Content,
				Concepts: p.Concepts, Files: p.Files,
			}
			if err := deps.Store.InsertMemory(ctx, row); err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]string{"memoryId": id}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "forget",
		Description: "Hard-delete a memory by id (writes to the audit log).",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{"memoryId"},
			"properties": map[string]any{
				"memoryId": map[string]any{"type": "string"},
				"reason":   map[string]any{"type": "string"},
				"actor":    map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			id, _ := args["memoryId"].(string)
			reason, _ := args["reason"].(string)
			actor, _ := args["actor"].(string)
			if id == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "memoryId is required"}
			}
			if actor == "" {
				actor = "mcp"
			}
			payload, _ := json.Marshal(map[string]string{"reason": reason})
			_, _ = deps.Store.DB.ExecContext(ctx,
				`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
                 VALUES (datetime('now'), ?, 'delete', 'memory', ?, ?)`, actor, id, string(payload))
			if err := deps.Store.DeleteMemory(ctx, id); err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]string{"status": "deleted", "memoryId": id}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "session-history",
		Description: "List recent sessions (most recent first), optionally filtered by project.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			limit := 25
			if v, ok := args["limit"].(float64); ok && v > 0 {
				limit = int(v)
			}
			rows, err := deps.Store.DB.QueryContext(ctx, `
                SELECT id, project, status, observation_count, started_at, COALESCE(ended_at,'')
                FROM sessions
                WHERE (? = '' OR project = ?)
                ORDER BY started_at DESC LIMIT ?
            `, project, project, limit)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			defer func() { _ = rows.Close() }()
			var out []map[string]any
			for rows.Next() {
				var id, proj, status, started, ended string
				var count int
				if err := rows.Scan(&id, &proj, &status, &count, &started, &ended); err != nil {
					return nil, &Error{Code: ErrInternal, Message: err.Error()}
				}
				out = append(out, map[string]any{
					"id": id, "project": proj, "status": status,
					"observationCount": count, "startedAt": started, "endedAt": ended,
				})
			}
			return map[string]any{"sessions": out}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "status",
		Description: "Report agent_mem health: counts of sessions/observations/memories and schema version.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			var sessions, obs, mems int
			_ = deps.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessions)
			_ = deps.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).Scan(&obs)
			_ = deps.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&mems)
			return map[string]any{
				"schemaVersion":   deps.Store.SchemaVersion,
				"sessionsCount":   sessions,
				"observationsCount": obs,
				"memoriesCount":   mems,
			}, nil
		},
	})
}

// RegisterMemoryResources exposes a couple of read-only browsable resources.
func RegisterMemoryResources(s *Server, deps MemoryToolsDeps) {
	s.RegisterResource(ResourceDefinition{
		URI:         "agentmemory://status",
		Name:        "status",
		Description: "agent_mem health snapshot",
		MIMEType:    "application/json",
		Read: func(ctx context.Context) (string, *Error) {
			var sessions, obs, mems int
			_ = deps.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessions)
			_ = deps.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations`).Scan(&obs)
			_ = deps.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&mems)
			b, _ := json.Marshal(map[string]any{
				"schemaVersion": deps.Store.SchemaVersion,
				"counts":        map[string]int{"sessions": sessions, "observations": obs, "memories": mems},
			})
			return string(b), nil
		},
	})
	s.RegisterResource(ResourceDefinition{
		URI:         "agentmemory://memories/latest",
		Name:        "memories.latest",
		Description: "Latest 25 memories (any project)",
		MIMEType:    "application/json",
		Read: func(ctx context.Context) (string, *Error) {
			ms, err := deps.Store.ListMemoriesByTier(ctx, "", "", 25)
			if err != nil {
				return "", &Error{Code: ErrInternal, Message: err.Error()}
			}
			b, _ := json.Marshal(ms)
			return string(b), nil
		},
	})
}

// RegisterMemoryPrompts registers /recall, /remember, /session-history,
// /forget as MCP prompts. Each is a one-line template that an agent can
// expand client-side; the heavy lifting is the corresponding tool.
func RegisterMemoryPrompts(s *Server) {
	prompts := []struct{ name, desc, template string }{
		{"recall", "Recall a memory by id.", "Recall memory {{memoryId}} and inject its content into context."},
		{"remember", "Save a new memory.", "Save a memory with title {{title}} and content {{content}}."},
		{"session-history", "Show recent sessions.", "List the most recent sessions in this project."},
		{"forget", "Delete a memory.", "Delete memory {{memoryId}} because {{reason}}."},
	}
	for _, p := range prompts {
		p := p
		s.RegisterPrompt(PromptDefinition{
			Name: p.name, Description: p.desc,
			Render: func(ctx context.Context, args map[string]any) (any, *Error) {
				return map[string]any{
					"description": p.desc,
					"messages": []map[string]any{{
						"role": "user",
						"content": map[string]any{"type": "text", "text": p.template},
					}},
				}, nil
			},
		})
	}
}

func newID(prefix string) string {
	return prefix + "-" + randomHex(8)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = cryptoRandRead(b)
	const hex = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0xf]
	}
	return string(out)
}
