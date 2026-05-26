package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// MemoryAPI groups handlers for /agentmemory/memories, /remember, /forget.
type MemoryAPI struct {
	Store *state.Store
}

// Register attaches memory routes.
func (a MemoryAPI) Register(r chi.Router) {
	r.Get("/memories", a.List)
	r.Get("/memory/{id}", a.Get)
	r.Post("/remember", a.Remember)
	r.Post("/forget", a.Forget)
}

// RememberRequest writes a new Memory row from a JSON body.
type RememberRequest struct {
	Project              string   `json:"project,omitempty"`
	Type                 string   `json:"type"`
	Tier                 string   `json:"tier,omitempty"`
	Title                string   `json:"title"`
	Content              string   `json:"content"`
	Concepts             []string `json:"concepts,omitempty"`
	Files                []string `json:"files,omitempty"`
	SessionIDs           []string `json:"sessionIds,omitempty"`
	SourceObservationIDs []string `json:"sourceObservationIds,omitempty"`
	Strength             float64  `json:"strength,omitempty"`
}

// Remember persists a new memory; returns the assigned id.
func (a MemoryAPI) Remember(w http.ResponseWriter, r *http.Request) {
	var req RememberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.Title == "" || req.Type == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "type and title required")
		return
	}
	if req.Tier == "" {
		req.Tier = state.TierSemantic
	}
	id := "mem-" + randHex(8)
	row := &state.MemoryRow{
		ID:                   id,
		Project:              req.Project,
		Type:                 req.Type,
		Tier:                 req.Tier,
		Title:                req.Title,
		Content:              req.Content,
		Concepts:             req.Concepts,
		Files:                req.Files,
		SessionIDs:           req.SessionIDs,
		SourceObservationIDs: req.SourceObservationIDs,
		Strength:             req.Strength,
	}
	if err := a.Store.InsertMemory(r.Context(), row); err != nil {
		WriteError(w, http.StatusInternalServerError, "persist_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"memoryId": id})
}

// Get returns a single memory by id; marks it as retrieved (for decay reinforcement).
func (a MemoryAPI) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := a.Store.GetMemory(r.Context(), id)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "memory not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	_ = a.Store.MarkRetrieved(r.Context(), []string{id})
	writeJSON(w, http.StatusOK, m)
}

// List returns memories filtered by project + tier; ordered by strength.
func (a MemoryAPI) List(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	tier := r.URL.Query().Get("tier")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	rows, err := a.Store.ListMemoriesByTier(r.Context(), project, tier, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	// Mark all returned memories as retrieved.
	ids := make([]string, len(rows))
	for i, m := range rows {
		ids[i] = m.ID
	}
	_ = a.Store.MarkRetrieved(r.Context(), ids)
	writeJSON(w, http.StatusOK, map[string]any{"memories": rows})
}

// ForgetRequest deletes a memory by id and records the audit entry.
type ForgetRequest struct {
	MemoryID string `json:"memoryId"`
	Actor    string `json:"actor,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Forget deletes a memory and writes the action to the audit log.
func (a MemoryAPI) Forget(w http.ResponseWriter, r *http.Request) {
	var req ForgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.MemoryID == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "memoryId required")
		return
	}
	if req.Actor == "" {
		req.Actor = "anonymous"
	}
	payload, _ := json.Marshal(map[string]string{"reason": req.Reason})
	// Audit first; then delete.
	if _, err := a.Store.DB.ExecContext(r.Context(),
		`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
         VALUES (datetime('now'), ?, 'delete', 'memory', ?, ?)`,
		req.Actor, req.MemoryID, string(payload),
	); err != nil {
		WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error())
		return
	}
	if err := a.Store.DeleteMemory(r.Context(), req.MemoryID); err != nil {
		WriteError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
