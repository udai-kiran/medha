package mcp

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
)

// cryptoRandRead is a one-line wrapper so the call site reads cleanly.
var cryptoRandRead = cryptorand.Read

// MemoryToolsDeps bundles the dependencies the agent_mem MCP tools need.
// Mirrors api.RouterDeps but narrow to what tools call directly.
type MemoryToolsDeps struct {
	Store         *state.Store
	Search        *search.Hybrid
	PythonBaseURL string // e.g. http://localhost:5000 — for LLM title generation
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
		Name:        "timeline",
		Description: "Chronological observations with cursor-based pagination. Supports filtering by session, hook type, and file path.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"session":  map[string]any{"type": "string"},
				"hookType": map[string]any{"type": "string"},
				"filePath": map[string]any{"type": "string"},
				"after":    map[string]any{"type": "string", "description": "ISO-8601 cursor — return entries after this timestamp"},
				"before":   map[string]any{"type": "string"},
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			var p struct {
				Project  string  `json:"project"`
				Session  string  `json:"session"`
				HookType string  `json:"hookType"`
				FilePath string  `json:"filePath"`
				After    string  `json:"after"`
				Before   string  `json:"before"`
				Limit    float64 `json:"limit"`
			}
			if e := MustParseArgs(args, &p); e != nil {
				return nil, e
			}
			entries, err := deps.Store.Timeline(ctx, state.TimelineFilter{
				Project:  p.Project,
				Session:  p.Session,
				HookType: p.HookType,
				FilePath: p.FilePath,
				After:    p.After,
				Before:   p.Before,
				Limit:    int(p.Limit),
			})
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			var nextCursor string
			if len(entries) > 0 && int(p.Limit) > 0 && len(entries) == int(p.Limit) {
				nextCursor = entries[len(entries)-1].CreatedAt
			}
			return map[string]any{"timeline": entries, "nextCursor": nextCursor}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "patterns",
		Description: "Detect and return recurring concept and file patterns across observations for a project.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"detect":  map[string]any{"type": "boolean", "description": "Re-run detection before returning results (slower but fresh)"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			limit, _ := args["limit"].(float64)
			detect, _ := args["detect"].(bool)
			lim := int(limit)
			var (
				rows []*state.PatternRow
				err  error
			)
			if detect {
				rows, err = deps.Store.DetectAndSavePatterns(ctx, project, lim)
			} else {
				rows, err = deps.Store.ListPatterns(ctx, project, lim)
				if err == nil && len(rows) == 0 {
					rows, err = deps.Store.DetectAndSavePatterns(ctx, project, lim)
				}
			}
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"patterns": rows}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "profile",
		Description: "Project intelligence snapshot: top concepts, top files, memory type distribution, and counts.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			profile, err := deps.Store.ProjectProfile(ctx, project)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return profile, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "file-history",
		Description: "Chronological list of compressed observations that touched a given file path.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"filePath"},
			"properties": map[string]any{
				"filePath": map[string]any{"type": "string", "description": "File path to look up (exact match within files array)"},
				"project":  map[string]any{"type": "string"},
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			var p struct {
				FilePath string `json:"filePath"`
				Project  string `json:"project"`
				Limit    int    `json:"limit"`
			}
			if e := MustParseArgs(args, &p); e != nil {
				return nil, e
			}
			if p.FilePath == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "filePath is required"}
			}
			entries, err := deps.Store.FileHistory(ctx, p.Project, p.FilePath, p.Limit)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			out := make([]map[string]any, 0, len(entries))
			for _, e := range entries {
				out = append(out, map[string]any{
					"id": e.ID, "sessionId": e.SessionID, "hookType": e.HookType,
					"toolName": e.ToolName, "type": e.Type, "title": e.Title,
					"createdAt": e.CreatedAt,
				})
			}
			return map[string]any{"filePath": p.FilePath, "history": out}, nil
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
		Description: "Persist a new memory. Only content is required; type defaults to 'fact' and title is auto-generated when omitted.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"content"},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"type":     map[string]any{"type": "string", "description": "Memory type, e.g. fact, preference, project, feedback. Defaults to 'fact'."},
				"title":    map[string]any{"type": "string", "description": "Short title. Auto-generated from content when omitted."},
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
			if p.Content == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "content is required"}
			}
			if p.Type == "" {
				p.Type = "fact"
			}
			if p.Title == "" {
				if deps.PythonBaseURL != "" {
					if t := llmTitle(ctx, deps.PythonBaseURL, p.Content); t != "" {
						p.Title = t
					}
				}
				if p.Title == "" {
					p.Title = autoTitle(p.Content)
				}
			}
			if p.Tier == "" {
				p.Tier = state.TierSemantic
			}

			// Dedup: search existing memories before inserting.
			similar, _ := deps.Store.SearchMemoriesByText(ctx, p.Project, p.Content, 5)
			var dupHints []map[string]any
			for _, m := range similar {
				if titleSimilarity(m.Title, p.Title) >= 0.6 || contentOverlap(m.Content, p.Content) >= 0.5 {
					dupHints = append(dupHints, map[string]any{
						"memoryId": m.ID,
						"title":    m.Title,
						"strength": m.Strength,
					})
				}
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
			result := map[string]any{"memoryId": id, "title": p.Title}
			if len(dupHints) > 0 {
				result["similarMemories"] = dupHints
				result["warning"] = "similar memories already exist — consider updating instead of creating"
			}
			return result, nil
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
                 VALUES ($1, $2, 'delete', 'memory', $3, $4)`,
				time.Now().UTC().Format(time.RFC3339Nano), actor, id, string(payload))
			if err := deps.Store.DeleteMemory(ctx, id); err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]string{"status": "deleted", "memoryId": id}, nil
		},
	})

	// G08: Short-term conversation memory.
	s.RegisterTool(ToolDefinition{
		Name:        "store-message",
		Description: "Store a conversation message (user/assistant/system) in short-term memory.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sessionId", "role", "content"},
			"properties": map[string]any{
				"sessionId": map[string]any{"type": "string"},
				"project":   map[string]any{"type": "string"},
				"role":      map[string]any{"type": "string", "enum": []string{"user", "assistant", "system"}},
				"content":   map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			sessID, _ := args["sessionId"].(string)
			project, _ := args["project"].(string)
			role, _ := args["role"].(string)
			content, _ := args["content"].(string)
			if sessID == "" || role == "" || content == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "sessionId, role, and content are required"}
			}
			msg, err := deps.Store.AddMessage(ctx, sessID, project, role, content, nil)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return msg, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "get-conversation",
		Description: "Retrieve the full conversation history for a session.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sessionId"},
			"properties": map[string]any{
				"sessionId": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			sessID, _ := args["sessionId"].(string)
			if sessID == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "sessionId is required"}
			}
			conv, err := deps.Store.GetConversation(ctx, sessID)
			if err == state.ErrNotFound {
				return map[string]any{"messages": []any{}}, nil
			}
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return conv, nil
		},
	})

	// G09: Preferences.
	s.RegisterTool(ToolDefinition{
		Name:        "add-preference",
		Description: "Record a user preference (category + preference text).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"category", "preference"},
			"properties": map[string]any{
				"project":    map[string]any{"type": "string"},
				"category":   map[string]any{"type": "string"},
				"preference": map[string]any{"type": "string"},
				"confidence": map[string]any{"type": "number"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			category, _ := args["category"].(string)
			pref, _ := args["preference"].(string)
			conf, _ := args["confidence"].(float64)
			if category == "" || pref == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "category and preference are required"}
			}
			row, err := deps.Store.AddPreference(ctx, project, category, pref, conf, nil)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return row, nil
		},
	})

	// G10: Facts.
	s.RegisterTool(ToolDefinition{
		Name:        "add-fact",
		Description: "Store a subject–predicate–object fact triple.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"subject", "predicate", "object"},
			"properties": map[string]any{
				"project":    map[string]any{"type": "string"},
				"subject":    map[string]any{"type": "string"},
				"predicate":  map[string]any{"type": "string"},
				"object":     map[string]any{"type": "string"},
				"confidence": map[string]any{"type": "number"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			subject, _ := args["subject"].(string)
			predicate, _ := args["predicate"].(string)
			objectVal, _ := args["object"].(string)
			conf, _ := args["confidence"].(float64)
			if subject == "" || predicate == "" || objectVal == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "subject, predicate, and object are required"}
			}
			row, err := deps.Store.AddFact(ctx, project, subject, predicate, objectVal, conf)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return row, nil
		},
	})

	// G11: Reasoning traces.
	s.RegisterTool(ToolDefinition{
		Name:        "start-trace",
		Description: "Start a reasoning trace for audit and future reference.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sessionId", "task"},
			"properties": map[string]any{
				"sessionId": map[string]any{"type": "string"},
				"project":   map[string]any{"type": "string"},
				"task":      map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			sessID, _ := args["sessionId"].(string)
			project, _ := args["project"].(string)
			task, _ := args["task"].(string)
			if sessID == "" || task == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "sessionId and task are required"}
			}
			trace, err := deps.Store.StartTrace(ctx, sessID, project, task, nil)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return trace, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "record-step",
		Description: "Append a reasoning step (thought/action/observation) to a trace.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"traceId", "thought"},
			"properties": map[string]any{
				"traceId":     map[string]any{"type": "string"},
				"thought":     map[string]any{"type": "string"},
				"action":      map[string]any{"type": "string"},
				"observation": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			traceID, _ := args["traceId"].(string)
			thought, _ := args["thought"].(string)
			action, _ := args["action"].(string)
			obs, _ := args["observation"].(string)
			if traceID == "" || thought == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "traceId and thought are required"}
			}
			step, err := deps.Store.RecordStep(ctx, traceID, thought, action, obs)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return step, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "complete-trace",
		Description: "Mark a reasoning trace as completed with an outcome summary.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"traceId"},
			"properties": map[string]any{
				"traceId": map[string]any{"type": "string"},
				"outcome": map[string]any{"type": "string"},
				"success": map[string]any{"type": "boolean"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			traceID, _ := args["traceId"].(string)
			outcome, _ := args["outcome"].(string)
			success, _ := args["success"].(bool)
			if traceID == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "traceId is required"}
			}
			if err := deps.Store.CompleteTrace(ctx, traceID, outcome, success); err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"completed": true, "traceId": traceID}, nil
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
                WHERE ($1 = '' OR project = $1)
                ORDER BY started_at DESC LIMIT $2
            `, project, limit)
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

	// G34: Unified context assembly.
	s.RegisterTool(ToolDefinition{
		Name:        "get-context",
		Description: "Assemble injection-ready context from memories, preferences, facts, conversation history, and slots.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project":          map[string]any{"type": "string"},
				"sessionId":        map[string]any{"type": "string"},
				"query":            map[string]any{"type": "string"},
				"includeShortTerm": map[string]any{"type": "boolean"},
				"includeLongTerm":  map[string]any{"type": "boolean"},
				"includeReasoning": map[string]any{"type": "boolean"},
				"includeSlots":     map[string]any{"type": "boolean"},
				"maxItems":         map[string]any{"type": "integer"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			sessionID, _ := args["sessionId"].(string)
			query, _ := args["query"].(string)
			incShort, _ := args["includeShortTerm"].(bool)
			incLong := true
			if v, ok := args["includeLongTerm"].(bool); ok {
				incLong = v
			}
			incReason, _ := args["includeReasoning"].(bool)
			incSlots := true
			if v, ok := args["includeSlots"].(bool); ok {
				incSlots = v
			}
			maxItems, _ := args["maxItems"].(float64)
			result, err := deps.Store.AssembleContext(ctx, state.ContextRequest{
				Project: project, SessionID: sessionID, Query: query,
				IncludeShortTerm: incShort, IncludeLongTerm: incLong,
				IncludeReasoning: incReason, IncludeSlots: incSlots,
				MaxItems: int(maxItems),
			})
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return result, nil
		},
	})

	// G27: Lessons.
	s.RegisterTool(ToolDefinition{
		Name:        "search-lessons",
		Description: "Search lessons extracted from past sessions by topic.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"topic":   map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			topic, _ := args["topic"].(string)
			limit, _ := args["limit"].(float64)
			rows, err := deps.Store.SearchLessons(ctx, project, topic, int(limit))
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"lessons": rows}, nil
		},
	})

	// G28: Skills.
	s.RegisterTool(ToolDefinition{
		Name:        "search-skills",
		Description: "Search acquired skills by name.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project":   map[string]any{"type": "string"},
				"skillName": map[string]any{"type": "string"},
				"limit":     map[string]any{"type": "integer"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			skill, _ := args["skillName"].(string)
			limit, _ := args["limit"].(float64)
			rows, err := deps.Store.SearchSkills(ctx, project, skill, int(limit))
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"skills": rows}, nil
		},
	})

	// G24: Slots.
	s.RegisterTool(ToolDefinition{
		Name:        "slot-set",
		Description: "Set a named pinned memory slot (persona, preferences, guidance, pending_items).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slotName", "content"},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"slotName": map[string]any{"type": "string"},
				"content":  map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			name, _ := args["slotName"].(string)
			content, _ := args["content"].(string)
			if name == "" {
				return nil, &Error{Code: ErrInvalidParams, Message: "slotName is required"}
			}
			if err := deps.Store.SetSlot(ctx, project, name, content); err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"slotName": name, "updated": true}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "slot-get",
		Description: "Retrieve a named pinned memory slot.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"slotName"},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"slotName": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			name, _ := args["slotName"].(string)
			content, err := deps.Store.GetSlot(ctx, project, name)
			if err == state.ErrNotFound {
				return map[string]any{"slotName": name, "content": ""}, nil
			}
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"slotName": name, "content": content}, nil
		},
	})

	// G25: Working memory.
	s.RegisterTool(ToolDefinition{
		Name:        "working-push",
		Description: "Push ephemeral context onto the session-scoped working memory stack.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sessionId", "content"},
			"properties": map[string]any{
				"sessionId": map[string]any{"type": "string"},
				"content":   map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			sessID, _ := args["sessionId"].(string)
			content, _ := args["content"].(string)
			id, err := deps.Store.WorkingPush(ctx, sessID, content)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"workingId": id}, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "working-pop",
		Description: "Pop items from the working memory stack.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sessionId"},
			"properties": map[string]any{
				"sessionId": map[string]any{"type": "string"},
				"count":     map[string]any{"type": "integer"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			sessID, _ := args["sessionId"].(string)
			count, _ := args["count"].(float64)
			items, err := deps.Store.WorkingPop(ctx, sessID, int(count))
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return map[string]any{"items": items}, nil
		},
	})

	// G06: Export convenience tool.
	s.RegisterTool(ToolDefinition{
		Name:        "export",
		Description: "Export all memories and sessions for a project as a JSON bundle.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, *Error) {
			project, _ := args["project"].(string)
			bundle, err := deps.Store.Export(ctx, project)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return bundle, nil
		},
	})

	// G07: Diagnose tool.
	s.RegisterTool(ToolDefinition{
		Name:        "diagnose",
		Description: "Run system health checks and return a diagnostic report.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, _ map[string]any) (any, *Error) {
			report, err := deps.Store.Diagnose(ctx)
			if err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			return report, nil
		},
	})

	s.RegisterTool(ToolDefinition{
		Name:        "status",
		Description: "Report agent_mem health: counts of sessions/observations/memories and schema version.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler: func(ctx context.Context, _ map[string]any) (any, *Error) {
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

// autoTitle derives a short title from the first 8 words of content.
func autoTitle(content string) string {
	words := strings.Fields(content)
	if len(words) > 8 {
		words = words[:8]
	}
	title := strings.Join(words, " ")
	if len(title) > 60 {
		title = title[:57] + "..."
	}
	return title
}

// llmTitle calls the Python /title endpoint to get an LLM-generated title.
// Returns empty string on any error so callers fall back to autoTitle.
func llmTitle(ctx context.Context, pythonBaseURL, content string) string {
	type req struct {
		Content string `json:"content"`
	}
	type resp struct {
		Title string `json:"title"`
	}
	body, err := json.Marshal(req{Content: content})
	if err != nil {
		return ""
	}
	url := strings.TrimRight(pythonBaseURL, "/") + "/title"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ""
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return ""
	}
	defer func() { _ = httpResp.Body.Close() }()
	if httpResp.StatusCode != http.StatusOK {
		return ""
	}
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return ""
	}
	var r resp
	if err := json.Unmarshal(raw, &r); err != nil {
		return ""
	}
	return strings.TrimSpace(r.Title)
}

// titleSimilarity returns a rough overlap score between two titles (0–1).
// Uses word Jaccard similarity.
func titleSimilarity(a, b string) float64 {
	wa := wordSet(strings.ToLower(a))
	wb := wordSet(strings.ToLower(b))
	if len(wa) == 0 && len(wb) == 0 {
		return 1
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	union := len(wa) + len(wb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// contentOverlap returns a bigram-overlap score between two content strings (0–1).
func contentOverlap(a, b string) float64 {
	wa := strings.Fields(strings.ToLower(a))
	wb := strings.Fields(strings.ToLower(b))
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	// Use unique word overlap as a simple proxy.
	sa := wordSet(strings.Join(wa, " "))
	sb := wordSet(strings.Join(wb, " "))
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func wordSet(s string) map[string]bool {
	words := strings.Fields(s)
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
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
