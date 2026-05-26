package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// SessionAPI groups handlers for /agentmemory/session* + /agentmemory/sessions.
type SessionAPI struct {
	Store      *state.Store
	SessionEnd SessionEndHandler
}

// Register attaches the session routes under the parent router.
func (a SessionAPI) Register(r chi.Router) {
	r.Post("/session/start", a.Start)
	r.Post("/session/end", a.End)
	r.Get("/session/{id}", a.Get)
	r.Get("/sessions", a.List)
}

// SessionStartRequest is the body for /session/start.
type SessionStartRequest struct {
	SessionID string `json:"sessionId"`
	Project   string `json:"project,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

// Start ensures a session exists; returns 200 with the session.
func (a SessionAPI) Start(w http.ResponseWriter, r *http.Request) {
	var req SessionStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.SessionID == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "sessionId required")
		return
	}
	row, err := a.Store.EnsureSession(r.Context(), req.SessionID, req.Project, req.CWD)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "session_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// SessionEndRequest is the body for /session/end.
type SessionEndRequest struct {
	SessionID   string `json:"sessionId"`
	SummaryHint string `json:"summaryHint,omitempty"`
}

// End marks the session ended and routes to consolidation (best-effort).
func (a SessionAPI) End(w http.ResponseWriter, r *http.Request) {
	var req SessionEndRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.SessionID == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "sessionId required")
		return
	}
	// Run consolidation asynchronously so the caller isn't blocked.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = a.SessionEnd.OnSessionEnd(ctx, req.SessionID)
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "consolidating"})
}

// Get returns a session by id.
func (a SessionAPI) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := a.Store.GetSession(r.Context(), id)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "session_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// List returns recent sessions, optionally filtered by project.
func (a SessionAPI) List(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	rows, err := a.Store.DB.QueryContext(r.Context(), `
        SELECT id, project, COALESCE(cwd,''), status, observation_count, started_at, updated_at, ended_at
        FROM sessions WHERE (? = '' OR project = ?)
        ORDER BY started_at DESC LIMIT ?
    `, project, project, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var id, proj, cwd, status, started, updated string
		var ended *string
		var count int
		if err := rows.Scan(&id, &proj, &cwd, &status, &count, &started, &updated, &ended); err != nil {
			WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		entry := map[string]any{
			"id": id, "project": proj, "cwd": cwd, "status": status,
			"observationCount": count, "startedAt": started, "updatedAt": updated,
		}
		if ended != nil {
			entry["endedAt"] = *ended
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}
