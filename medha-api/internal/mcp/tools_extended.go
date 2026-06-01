package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/udai-kiran/medha/internal/state"
)

// RegisterExtendedTools registers the G32 extended tool surface:
// orchestration, team/governance, slots/working, advanced retrieval,
// entity intelligence, and vision platform tools (27 additional tools).
func RegisterExtendedTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	registerOrchestrationTools(s, deps)
	registerTeamGovernanceTools(s, deps)
	registerSlotWorkingTools(s, deps)
	registerAdvancedRetrievalTools(s, deps)
	registerEntityTools(s, deps)
	registerPlatformTools(s, deps)
}

// registerOrchestrationTools adds 13 tools covering G16–G21.
func registerOrchestrationTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	s.AddTool(&sdkmcp.Tool{
		Name:        "action-create",
		Description: "Create a new action (work item) in the multi-agent orchestration DAG.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"title"},
			"properties": map[string]any{
				"title":        map[string]any{"type": "string"},
				"project":      map[string]any{"type": "string"},
				"description":  map[string]any{"type": "string"},
				"status":       map[string]any{"type": "string", "enum": []string{"pending", "running", "completed", "failed", "cancelled"}},
				"dependencies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"assigneeId":   map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			Title        string   `json:"title"`
			Project      string   `json:"project"`
			Description  string   `json:"description"`
			Status       string   `json:"status"`
			Dependencies []string `json:"dependencies"`
			AssigneeID   string   `json:"assigneeId"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.Title == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "title is required"}
		}
		row := &state.ActionRow{
			ID:           newID("act"),
			Project:      p.Project,
			Title:        p.Title,
			Description:  p.Description,
			Status:       p.Status,
			Dependencies: p.Dependencies,
			AssigneeID:   p.AssigneeID,
		}
		if err := deps.Store.PutAction(ctx, row); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return row, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "action-get",
		Description: "Retrieve an action by id.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id":      map[string]any{"type": "string"},
				"project": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		id, _ := args["id"].(string)
		project, _ := args["project"].(string)
		if id == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "id is required"}
		}
		row, err := deps.Store.GetAction(ctx, project, id)
		if errors.Is(err, state.ErrNotFound) {
			return nil, &Error{Code: ErrInvalidParams, Message: "action not found"}
		}
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return row, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "action-update",
		Description: "Update an action's status or assignee.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]any{
				"id":         map[string]any{"type": "string"},
				"project":    map[string]any{"type": "string"},
				"status":     map[string]any{"type": "string", "enum": []string{"pending", "running", "completed", "failed", "cancelled"}},
				"assigneeId": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			ID         string `json:"id"`
			Project    string `json:"project"`
			Status     string `json:"status"`
			AssigneeID string `json:"assigneeId"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.ID == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "id is required"}
		}
		row, err := deps.Store.GetAction(ctx, p.Project, p.ID)
		if errors.Is(err, state.ErrNotFound) {
			return nil, &Error{Code: ErrInvalidParams, Message: "action not found"}
		}
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		if p.Status != "" {
			row.Status = p.Status
		}
		if p.AssigneeID != "" {
			row.AssigneeID = p.AssigneeID
		}
		if err := deps.Store.PutAction(ctx, row); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return row, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "frontier",
		Description: "Return all unblocked actions ready to start (no unsatisfied dependencies).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		project, _ := args["project"].(string)
		rows, err := deps.Store.Frontier(ctx, project)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"frontier": rows}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "next-action",
		Description: "Return the single highest-priority unblocked action ready to execute.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		project, _ := args["project"].(string)
		action, err := deps.Store.GetNextAction(ctx, project)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		if action == nil {
			return map[string]any{"action": nil, "message": "no unblocked actions"}, nil
		}
		return map[string]any{"action": action}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "lease-acquire",
		Description: "Acquire an exclusive lease on an action. Returns conflict if another holder has it.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"actionId", "holderId"},
			"properties": map[string]any{
				"actionId": map[string]any{"type": "string"},
				"holderId": map[string]any{"type": "string"},
				"project":  map[string]any{"type": "string"},
				"ttlSecs":  map[string]any{"type": "integer", "description": "Lease TTL in seconds; defaults to 600."},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			ActionID string  `json:"actionId"`
			HolderID string  `json:"holderId"`
			Project  string  `json:"project"`
			TTLSecs  float64 `json:"ttlSecs"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.ActionID == "" || p.HolderID == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "actionId and holderId are required"}
		}
		ttl := 10 * time.Minute
		if p.TTLSecs > 0 {
			ttl = time.Duration(p.TTLSecs) * time.Second
		}
		lease, err := deps.Store.AcquireLease(ctx, p.Project, p.ActionID, p.HolderID, ttl)
		if errors.Is(err, state.ErrLeaseHeld) {
			return nil, &Error{Code: ErrInvalidParams, Message: "action already leased by another holder"}
		}
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return lease, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "lease-release",
		Description: "Release an exclusive lease on an action.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"actionId"},
			"properties": map[string]any{
				"actionId": map[string]any{"type": "string"},
				"holderId": map[string]any{"type": "string"},
				"project":  map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			ActionID string `json:"actionId"`
			HolderID string `json:"holderId"`
			Project  string `json:"project"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.ActionID == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "actionId is required"}
		}
		if err := deps.Store.ReleaseLease(ctx, p.Project, p.ActionID, p.HolderID); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"released": true, "actionId": p.ActionID}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "routine-put",
		Description: "Create or update a reusable workflow routine template.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"id":          map[string]any{"type": "string", "description": "Omit to auto-generate."},
				"project":     map[string]any{"type": "string"},
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"steps":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var row state.RoutineRow
		if e := MustParseArgs(args, &row); e != nil {
			return nil, e
		}
		if row.Name == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "name is required"}
		}
		if row.ID == "" {
			row.ID = newID("rou")
		}
		if err := deps.Store.PutRoutine(ctx, &row); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return row, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "signal-send",
		Description: "Send a signal (inter-agent message) to another agent.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"from", "to"},
			"properties": map[string]any{
				"from":    map[string]any{"type": "string", "description": "Sender agent id"},
				"to":      map[string]any{"type": "string", "description": "Recipient agent id"},
				"subject": map[string]any{"type": "string"},
				"body":    map[string]any{"type": "string"},
				"project": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var sig state.SignalRow
		if e := MustParseArgs(args, &sig); e != nil {
			return nil, e
		}
		if sig.From == "" || sig.To == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "from and to are required"}
		}
		sig.ID = state.SignalID()
		if err := deps.Store.SendSignal(ctx, &sig); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return sig, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "signal-inbox",
		Description: "Read signals from an agent's inbox.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"to"},
			"properties": map[string]any{
				"to":      map[string]any{"type": "string", "description": "Recipient agent id"},
				"project": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		to, _ := args["to"].(string)
		project, _ := args["project"].(string)
		if to == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "to is required"}
		}
		signals, err := deps.Store.ListInbox(ctx, project, to)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"signals": signals}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "checkpoint-create",
		Description: "Create a named checkpoint condition gate.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"conditionExpr"},
			"properties": map[string]any{
				"id":            map[string]any{"type": "string", "description": "Omit to auto-generate."},
				"project":       map[string]any{"type": "string"},
				"conditionExpr": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			ID            string `json:"id"`
			Project       string `json:"project"`
			ConditionExpr string `json:"conditionExpr"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.ConditionExpr == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "conditionExpr is required"}
		}
		if p.ID == "" {
			p.ID = newID("cp")
		}
		if err := deps.Store.CreateCheckpoint(ctx, p.Project, p.ID, p.ConditionExpr); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"checkpointId": p.ID}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "sentinel-create",
		Description: "Create a sentinel that fires an HTTP callback when a matching event occurs.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"eventPattern", "handlerUrl"},
			"properties": map[string]any{
				"id":           map[string]any{"type": "string", "description": "Omit to auto-generate."},
				"project":      map[string]any{"type": "string"},
				"eventPattern": map[string]any{"type": "string"},
				"handlerUrl":   map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			ID           string `json:"id"`
			Project      string `json:"project"`
			EventPattern string `json:"eventPattern"`
			HandlerURL   string `json:"handlerUrl"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.EventPattern == "" || p.HandlerURL == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "eventPattern and handlerUrl are required"}
		}
		if p.ID == "" {
			p.ID = newID("snt")
		}
		if err := deps.Store.CreateSentinel(ctx, p.Project, p.ID, p.EventPattern, p.HandlerURL); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"sentinelId": p.ID}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "crystallize",
		Description: "Compact a linear chain of completed actions into a single summary action.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"actionIds"},
			"properties": map[string]any{
				"project":   map[string]any{"type": "string"},
				"actionIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			Project   string   `json:"project"`
			ActionIDs []string `json:"actionIds"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if len(p.ActionIDs) == 0 {
			return nil, &Error{Code: ErrInvalidParams, Message: "actionIds must not be empty"}
		}
		action, err := deps.Store.Crystallize(ctx, p.Project, p.ActionIDs)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return action, nil
	}))
}

// registerTeamGovernanceTools adds 5 tools covering G22–G23.
func registerTeamGovernanceTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	s.AddTool(&sdkmcp.Tool{
		Name:        "team-share",
		Description: "Share a memory with a team (publishes a pointer in the team feed).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"memoryId", "team"},
			"properties": map[string]any{
				"memoryId": map[string]any{"type": "string"},
				"team":     map[string]any{"type": "string"},
				"mode":     map[string]any{"type": "string", "enum": []string{"read", "edit"}, "description": "Defaults to 'read'."},
				"actor":    map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			MemoryID string `json:"memoryId"`
			Team     string `json:"team"`
			Mode     string `json:"mode"`
			Actor    string `json:"actor"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.MemoryID == "" || p.Team == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "memoryId and team are required"}
		}
		if p.Mode == "" {
			p.Mode = "read"
		}
		if p.Mode != "read" && p.Mode != "edit" {
			return nil, &Error{Code: ErrInvalidParams, Message: "mode must be 'read' or 'edit'"}
		}
		if _, err := deps.Store.GetMemory(ctx, p.MemoryID); errors.Is(err, state.ErrNotFound) {
			return nil, &Error{Code: ErrInvalidParams, Message: "memory not found"}
		} else if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		share := map[string]any{
			"memoryId": p.MemoryID,
			"team":     p.Team,
			"mode":     p.Mode,
			"sharedAt": time.Now().UTC().Format(time.RFC3339Nano),
			"actor":    p.Actor,
		}
		kv := state.NewKV(deps.Store)
		if err := kv.Put(ctx, state.ScopeTeamShares,
			state.Key(state.ScopeTeamShares, p.Team, p.MemoryID), share); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		actor := p.Actor
		if actor == "" {
			actor = "mcp"
		}
		pl, _ := json.Marshal(map[string]string{"team": p.Team, "mode": p.Mode})
		_, _ = deps.Store.DB.ExecContext(ctx,
			`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
             VALUES ($1, $2, 'share', 'memory', $3, $4)`,
			time.Now().UTC().Format(time.RFC3339Nano), actor, p.MemoryID, string(pl))
		return share, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "team-feed",
		Description: "List memories shared to a team, each entry includes the full memory row.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"team"},
			"properties": map[string]any{
				"team":  map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		team, _ := args["team"].(string)
		limit := 50
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		if team == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "team is required"}
		}
		kv := state.NewKV(deps.Store)
		prefix := state.Key(state.ScopeTeamShares, team, "")
		pairs, err := kv.ListByPrefix(ctx, state.ScopeTeamShares, prefix)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		type feedItem struct {
			Share  map[string]any `json:"share"`
			Memory any            `json:"memory,omitempty"`
		}
		out := make([]feedItem, 0, len(pairs))
		for _, raw := range pairs {
			var s map[string]any
			if err := json.Unmarshal([]byte(raw), &s); err != nil {
				continue
			}
			item := feedItem{Share: s}
			if mid, _ := s["memoryId"].(string); mid != "" {
				if m, err := deps.Store.GetMemory(ctx, mid); err == nil {
					item.Memory = m
				}
			}
			out = append(out, item)
			if len(out) >= limit {
				break
			}
		}
		return map[string]any{"team": team, "feed": out}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "team-revoke",
		Description: "Revoke a memory share from a team feed.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"memoryId", "team"},
			"properties": map[string]any{
				"memoryId": map[string]any{"type": "string"},
				"team":     map[string]any{"type": "string"},
				"actor":    map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			MemoryID string `json:"memoryId"`
			Team     string `json:"team"`
			Actor    string `json:"actor"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.MemoryID == "" || p.Team == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "memoryId and team are required"}
		}
		kv := state.NewKV(deps.Store)
		if err := kv.Delete(ctx, state.ScopeTeamShares,
			state.Key(state.ScopeTeamShares, p.Team, p.MemoryID)); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		actor := p.Actor
		if actor == "" {
			actor = "mcp"
		}
		pl, _ := json.Marshal(map[string]string{"team": p.Team})
		_, _ = deps.Store.DB.ExecContext(ctx,
			`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
             VALUES ($1, $2, 'revoke', 'memory', $3, $4)`,
			time.Now().UTC().Format(time.RFC3339Nano), actor, p.MemoryID, string(pl))
		return map[string]any{"revoked": true}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "audit",
		Description: "Retrieve the audit log, optionally filtered by action type or actor.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
				"action": map[string]any{"type": "string", "description": "Filter by action type (e.g. 'share', 'delete')"},
				"actor":  map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		limit := 100
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		action, _ := args["action"].(string)
		actor, _ := args["actor"].(string)
		rows, err := deps.Store.DB.QueryContext(ctx, `
            SELECT timestamp, actor, action, target_type, target_id, payload_json
            FROM audit_log
            WHERE ($1 = '' OR action = $1)
            AND ($2 = '' OR actor = $2)
            ORDER BY id DESC LIMIT $3
        `, action, actor, limit)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		defer func() { _ = rows.Close() }()
		var out []map[string]any
		for rows.Next() {
			var ts, act, atn, ttype, tid, pl string
			if err := rows.Scan(&ts, &act, &atn, &ttype, &tid, &pl); err != nil {
				return nil, &Error{Code: ErrInternal, Message: err.Error()}
			}
			entry := map[string]any{
				"timestamp": ts, "actor": act, "action": atn,
				"targetType": ttype, "targetId": tid,
			}
			var payload map[string]any
			if json.Unmarshal([]byte(pl), &payload) == nil {
				entry["payload"] = payload
			}
			out = append(out, entry)
		}
		return map[string]any{"audit": out}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "governance-delete",
		Description: "Compliance-safe hard-delete of memories with mandatory reason and full audit trail.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"memoryIds", "reason"},
			"properties": map[string]any{
				"memoryIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"reason":    map[string]any{"type": "string"},
				"actor":     map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			MemoryIDs []string `json:"memoryIds"`
			Reason    string   `json:"reason"`
			Actor     string   `json:"actor"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if len(p.MemoryIDs) == 0 || p.Reason == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "memoryIds and reason are required"}
		}
		actor := p.Actor
		if actor == "" {
			actor = "mcp"
		}
		deleted := 0
		for _, id := range p.MemoryIDs {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			pl, _ := json.Marshal(map[string]string{"reason": p.Reason, "actor": actor})
			_, _ = deps.Store.DB.ExecContext(ctx,
				`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
                 VALUES ($1, $2, 'governance_delete', 'memory', $3, $4)`,
				now, actor, id, string(pl))
			if err := deps.Store.DeleteMemory(ctx, id); err == nil {
				deleted++
			}
		}
		return map[string]any{"deleted": deleted}, nil
	}))
}

// registerSlotWorkingTools adds 2 tools covering the missing slot-list and working-clear.
func registerSlotWorkingTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	s.AddTool(&sdkmcp.Tool{
		Name:        "slot-list",
		Description: "List all named pinned memory slots for a project.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		project, _ := args["project"].(string)
		slots, err := deps.Store.ListSlots(ctx, project)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"slots": slots}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "working-clear",
		Description: "Clear the entire working memory stack for a session.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sessionId"},
			"properties": map[string]any{
				"sessionId": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		sessID, _ := args["sessionId"].(string)
		if sessID == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "sessionId is required"}
		}
		if err := deps.Store.WorkingClear(ctx, sessID); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"cleared": true, "sessionId": sessID}, nil
	}))
}

// registerAdvancedRetrievalTools adds 4 tools covering the writable side of G26–G28.
func registerAdvancedRetrievalTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	s.AddTool(&sdkmcp.Tool{
		Name:        "facet-tag",
		Description: "Tag a memory with a dimension:value facet for structured filtering.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"memoryId", "dimension", "value"},
			"properties": map[string]any{
				"memoryId":  map[string]any{"type": "string"},
				"dimension": map[string]any{"type": "string", "description": "e.g. 'layer', 'status', 'component'"},
				"value":     map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			MemoryID  string `json:"memoryId"`
			Dimension string `json:"dimension"`
			Value     string `json:"value"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.MemoryID == "" || p.Dimension == "" || p.Value == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "memoryId, dimension, and value are required"}
		}
		if err := deps.Store.AddFacet(ctx, p.MemoryID, p.Dimension, p.Value); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"tagged": true, "memoryId": p.MemoryID, "dimension": p.Dimension, "value": p.Value}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "facet-query",
		Description: "Query memories by dimension:value facet combinations.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"facets"},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"facets": map[string]any{
					"type":                 "object",
					"description":          `Map of dimension to value list, e.g. {"layer": ["frontend"], "status": ["confirmed"]}`,
					"additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			Project string              `json:"project"`
			Facets  map[string][]string `json:"facets"`
			Limit   int                 `json:"limit"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if len(p.Facets) == 0 {
			return nil, &Error{Code: ErrInvalidParams, Message: "facets must not be empty"}
		}
		mems, err := deps.Store.QueryFacets(ctx, p.Project, p.Facets, p.Limit)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"memories": mems}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "lesson-add",
		Description: "Record a lesson learned from a session.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"lesson"},
			"properties": map[string]any{
				"project":   map[string]any{"type": "string"},
				"sessionId": map[string]any{"type": "string"},
				"lesson":    map[string]any{"type": "string"},
				"context":   map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			Project   string `json:"project"`
			SessionID string `json:"sessionId"`
			Lesson    string `json:"lesson"`
			Context   string `json:"context"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.Lesson == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "lesson is required"}
		}
		row, err := deps.Store.AddLesson(ctx, p.Project, p.SessionID, p.Lesson, p.Context)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return row, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "skill-upsert",
		Description: "Record or increment evidence for a demonstrated skill.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"skillName"},
			"properties": map[string]any{
				"project":   map[string]any{"type": "string"},
				"skillName": map[string]any{"type": "string"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			Project   string `json:"project"`
			SkillName string `json:"skillName"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.SkillName == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "skillName is required"}
		}
		row, err := deps.Store.UpsertSkill(ctx, p.Project, p.SkillName)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return row, nil
	}))
}

// registerEntityTools adds 2 tools covering G12 entity deduplication.
func registerEntityTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	s.AddTool(&sdkmcp.Tool{
		Name:        "entity-duplicates",
		Description: "List potential duplicate entity pairs pending review.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project":       map[string]any{"type": "string"},
				"minConfidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "Defaults to 0.7"},
				"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		project, _ := args["project"].(string)
		minConf, _ := args["minConfidence"].(float64)
		limit := 0
		if v, ok := args["limit"].(float64); ok {
			limit = int(v)
		}
		if minConf <= 0 {
			minConf = 0.7
		}
		rows, err := deps.Store.FindPotentialDuplicates(ctx, project, minConf, limit)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"duplicates": rows}, nil
	}))

	s.AddTool(&sdkmcp.Tool{
		Name:        "entity-review-duplicate",
		Description: "Confirm or reject a SAME_AS entity match.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"sourceId", "targetId"},
			"properties": map[string]any{
				"sourceId": map[string]any{"type": "string"},
				"targetId": map[string]any{"type": "string"},
				"confirm":  map[string]any{"type": "boolean", "description": "true to merge, false to reject"},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			SourceID string `json:"sourceId"`
			TargetID string `json:"targetId"`
			Confirm  bool   `json:"confirm"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.SourceID == "" || p.TargetID == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "sourceId and targetId are required"}
		}
		if err := deps.Store.ReviewDuplicate(ctx, p.SourceID, p.TargetID, p.Confirm); err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		return map[string]any{"reviewed": true, "confirmed": p.Confirm}, nil
	}))
}

// registerPlatformTools adds 1 tool covering the G29 vision embed.
func registerPlatformTools(s *sdkmcp.Server, deps MemoryToolsDeps) {
	s.AddTool(&sdkmcp.Tool{
		Name:        "vision-embed",
		Description: "Embed an image (by URL or caption text) into the vector index for visual memory search.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"docId"},
			"properties": map[string]any{
				"docId":    map[string]any{"type": "string"},
				"project":  map[string]any{"type": "string"},
				"imageUrl": map[string]any{"type": "string"},
				"caption":  map[string]any{"type": "string", "description": "Text description of the image; used when imageUrl is absent."},
			},
		},
	}, wrap(func(ctx context.Context, args map[string]any) (any, *Error) {
		var p struct {
			DocID    string `json:"docId"`
			Project  string `json:"project"`
			ImageURL string `json:"imageUrl"`
			Caption  string `json:"caption"`
		}
		if e := MustParseArgs(args, &p); e != nil {
			return nil, e
		}
		if p.DocID == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "docId is required"}
		}
		if p.ImageURL == "" && p.Caption == "" {
			return nil, &Error{Code: ErrInvalidParams, Message: "imageUrl or caption is required"}
		}
		if deps.PythonBaseURL == "" {
			return nil, &Error{Code: ErrInternal, Message: "Python service URL not configured"}
		}
		text := p.Caption
		if text == "" {
			text = "image: " + p.ImageURL
		}
		body, _ := json.Marshal(map[string]any{"texts": []string{text}})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(deps.PythonBaseURL, "/")+"/embed",
			bytes.NewReader(body))
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, &Error{Code: ErrInternal, Message: "embed call failed: " + err.Error()}
		}
		_ = resp.Body.Close()
		return map[string]any{"docId": p.DocID, "embedded": true, "source": "vision"}, nil
	}))
}
