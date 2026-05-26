package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// TeamAPI exposes /team/share, /team/feed, /audit (Task 32). All writes route
// through the audit log (FR-39).
//
// Scopes: each Memory has a project (its owner); sharing publishes a pointer
// row under the team's scope. Read access on the team feed checks the pointer
// rows + reads the underlying memory.
type TeamAPI struct {
	Store *state.Store
}

// Register attaches team routes.
func (a TeamAPI) Register(r chi.Router) {
	r.Post("/team/share", a.Share)
	r.Get("/team/feed", a.Feed)
	r.Post("/team/revoke", a.Revoke)
	r.Get("/audit", a.Audit)
}

// shareReq is the body of /team/share.
type shareReq struct {
	MemoryID string `json:"memoryId"`
	Team     string `json:"team"`
	Mode     string `json:"mode"`   // read | edit (default read)
	Actor    string `json:"actor,omitempty"`
}

// Share publishes a memory to a team's feed. The original memory row stays
// in the owner's project; the share is a typed pointer under team_shares.
func (a TeamAPI) Share(w http.ResponseWriter, r *http.Request) {
	var req shareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.MemoryID == "" || req.Team == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "memoryId and team required")
		return
	}
	if req.Mode == "" {
		req.Mode = "read"
	}
	if req.Mode != "read" && req.Mode != "edit" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "mode must be read or edit")
		return
	}

	// Verify the memory exists.
	if _, err := a.Store.GetMemory(r.Context(), req.MemoryID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			WriteError(w, http.StatusNotFound, "not_found", "memory not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}

	share := map[string]any{
		"memoryId":  req.MemoryID,
		"team":      req.Team,
		"mode":      req.Mode,
		"sharedAt":  time.Now().UTC().Format(time.RFC3339Nano),
		"actor":     req.Actor,
	}
	kv := state.NewKV(a.Store)
	if err := kv.Put(r.Context(), state.ScopeTeamShares,
		state.Key(state.ScopeTeamShares, req.Team, req.MemoryID), share); err != nil {
		WriteError(w, http.StatusInternalServerError, "persist_failed", err.Error())
		return
	}
	// Audit log.
	a.logAudit(r.Context(), req.Actor, "share", "memory", req.MemoryID, map[string]any{
		"team": req.Team, "mode": req.Mode,
	})
	writeJSON(w, http.StatusCreated, share)
}

// Feed returns memories shared to a team, plus the resolved memory rows.
func (a TeamAPI) Feed(w http.ResponseWriter, r *http.Request) {
	team := r.URL.Query().Get("team")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if team == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "team query param required")
		return
	}
	kv := state.NewKV(a.Store)
	prefix := state.Key(state.ScopeTeamShares, team, "")
	pairs, err := kv.ListByPrefix(r.Context(), state.ScopeTeamShares, prefix)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	type item struct {
		Share  map[string]any `json:"share"`
		Memory any            `json:"memory,omitempty"`
	}
	out := make([]item, 0, len(pairs))
	for _, raw := range pairs {
		var s map[string]any
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			continue
		}
		entry := item{Share: s}
		if mid, _ := s["memoryId"].(string); mid != "" {
			if m, err := a.Store.GetMemory(r.Context(), mid); err == nil {
				entry.Memory = m
			}
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team, "feed": out})
}

// Revoke removes a team share and writes the audit entry.
func (a TeamAPI) Revoke(w http.ResponseWriter, r *http.Request) {
	var req shareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.MemoryID == "" || req.Team == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "memoryId and team required")
		return
	}
	kv := state.NewKV(a.Store)
	if err := kv.Delete(r.Context(), state.ScopeTeamShares,
		state.Key(state.ScopeTeamShares, req.Team, req.MemoryID)); err != nil {
		WriteError(w, http.StatusInternalServerError, "revoke_failed", err.Error())
		return
	}
	a.logAudit(r.Context(), req.Actor, "revoke", "memory", req.MemoryID, map[string]any{"team": req.Team})
	w.WriteHeader(http.StatusNoContent)
}

// Audit returns the recent audit log.
func (a TeamAPI) Audit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	rows, err := a.Store.DB.QueryContext(r.Context(),
		`SELECT timestamp, actor, action, target_type, target_id, payload_json
         FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var ts, actor, action, ttype, tid, payload string
		if err := rows.Scan(&ts, &actor, &action, &ttype, &tid, &payload); err != nil {
			WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		entry := map[string]any{
			"timestamp": ts, "actor": actor, "action": action,
			"targetType": ttype, "targetId": tid,
		}
		var pl map[string]any
		if err := json.Unmarshal([]byte(payload), &pl); err == nil {
			entry["payload"] = pl
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": out})
}

func (a TeamAPI) logAudit(ctx context.Context, actor, action, targetType, targetID string, payload map[string]any) {
	if actor == "" {
		actor = "anonymous"
	}
	pl, _ := json.Marshal(payload)
	_, _ = a.Store.DB.ExecContext(ctx,
		`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
         VALUES (datetime('now'), ?, ?, ?, ?, ?)`,
		actor, action, targetType, targetID, string(pl),
	)
}
