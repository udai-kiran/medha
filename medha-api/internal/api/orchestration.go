package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// OrchestrationAPI exposes Actions, Leases, Routines, Signals (Task 31).
type OrchestrationAPI struct {
	Store *state.Store
}

// Register attaches the orchestration routes.
func (a OrchestrationAPI) Register(r chi.Router) {
	r.Get("/actions", a.ListActions)
	r.Post("/actions", a.CreateAction)
	r.Get("/actions/{id}", a.GetAction)
	r.Patch("/actions/{id}", a.PatchAction)

	r.Get("/frontier", a.Frontier)

	r.Post("/leases/{id}/acquire", a.AcquireLease)
	r.Post("/leases/{id}/release", a.ReleaseLease)

	r.Get("/routines", a.ListRoutines)
	r.Post("/routines", a.PutRoutine)

	r.Post("/signals", a.SendSignal)
	r.Get("/signals", a.ListInbox)

	// G20: Checkpoints + Sentinels.
	r.Post("/checkpoints", a.CreateCheckpoint)
	r.Post("/checkpoints/{id}/satisfy", a.SatisfyCheckpoint)
	r.Post("/sentinels", a.CreateSentinel)
	r.Post("/sentinels/{id}/trigger", a.TriggerSentinel)

	// G21: Sketches + Crystallization.
	r.Post("/sketches", a.CreateSketch)
	r.Post("/sketches/{id}/promote", a.PromoteSketch)
	r.Post("/actions/crystallize", a.Crystallize)
	r.Get("/next", a.NextAction)
}

// CreateAction body — server assigns id if absent.
type createActionReq struct {
	ID           string         `json:"id,omitempty"`
	Project      string         `json:"project,omitempty"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	Status       string         `json:"status,omitempty"`
	Dependencies []string       `json:"dependencies,omitempty"`
	AssigneeID   string         `json:"assigneeId,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// CreateAction persists a new action.
func (a OrchestrationAPI) CreateAction(w http.ResponseWriter, r *http.Request) {
	var req createActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.Title == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "title required")
		return
	}
	if req.ID == "" {
		req.ID = "act-" + randHex(8)
	}
	row := &state.ActionRow{
		ID: req.ID, Project: req.Project, Title: req.Title,
		Description: req.Description, Status: req.Status,
		Dependencies: req.Dependencies, AssigneeID: req.AssigneeID,
		Metadata: req.Metadata,
	}
	if err := a.Store.PutAction(r.Context(), row); err != nil {
		WriteError(w, http.StatusInternalServerError, "persist_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// GetAction returns an action by id.
func (a OrchestrationAPI) GetAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project := r.URL.Query().Get("project")
	row, err := a.Store.GetAction(r.Context(), project, id)
	if errors.Is(err, state.ErrNotFound) {
		WriteError(w, http.StatusNotFound, "not_found", "action not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// PatchAction updates status / assignee / metadata; preserves identity fields.
func (a OrchestrationAPI) PatchAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project := r.URL.Query().Get("project")
	row, err := a.Store.GetAction(r.Context(), project, id)
	if errors.Is(err, state.ErrNotFound) {
		WriteError(w, http.StatusNotFound, "not_found", "action not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	var patch struct {
		Status     *string         `json:"status,omitempty"`
		AssigneeID *string         `json:"assigneeId,omitempty"`
		Metadata   map[string]any  `json:"metadata,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if patch.Status != nil {
		row.Status = *patch.Status
	}
	if patch.AssigneeID != nil {
		row.AssigneeID = *patch.AssigneeID
	}
	if patch.Metadata != nil {
		row.Metadata = patch.Metadata
	}
	if err := a.Store.PutAction(r.Context(), row); err != nil {
		WriteError(w, http.StatusInternalServerError, "persist_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// ListActions returns all actions under a project.
func (a OrchestrationAPI) ListActions(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	rows, err := a.Store.ListActions(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": rows})
}

// Frontier returns the actions ready to start.
func (a OrchestrationAPI) Frontier(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	rows, err := a.Store.Frontier(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "frontier_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"frontier": rows})
}

// AcquireLease grants a lease for an action.
func (a OrchestrationAPI) AcquireLease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		HolderID  string `json:"holderId"`
		Project   string `json:"project"`
		TTLSecs   int    `json:"ttlSecs,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.HolderID == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "holderId required")
		return
	}
	ttl := 10 * time.Minute
	if req.TTLSecs > 0 {
		ttl = time.Duration(req.TTLSecs) * time.Second
	}
	lease, err := a.Store.AcquireLease(r.Context(), req.Project, id, req.HolderID, ttl)
	if errors.Is(err, state.ErrLeaseHeld) {
		WriteError(w, http.StatusConflict, "lease_held", err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "acquire_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lease)
}

// ReleaseLease drops the lease.
func (a OrchestrationAPI) ReleaseLease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		HolderID string `json:"holderId"`
		Project  string `json:"project"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := a.Store.ReleaseLease(r.Context(), req.Project, id, req.HolderID); err != nil {
		WriteError(w, http.StatusBadRequest, "release_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListRoutines / PutRoutine.
func (a OrchestrationAPI) ListRoutines(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	rs, err := a.Store.ListRoutines(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routines": rs})
}

// PutRoutine writes a routine.
func (a OrchestrationAPI) PutRoutine(w http.ResponseWriter, r *http.Request) {
	var row state.RoutineRow
	if err := json.NewDecoder(r.Body).Decode(&row); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if row.ID == "" {
		row.ID = "rou-" + randHex(8)
	}
	if err := a.Store.PutRoutine(r.Context(), &row); err != nil {
		WriteError(w, http.StatusBadRequest, "persist_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// SendSignal sends a signal between agents.
func (a OrchestrationAPI) SendSignal(w http.ResponseWriter, r *http.Request) {
	var sig state.SignalRow
	if err := json.NewDecoder(r.Body).Decode(&sig); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if sig.From == "" || sig.To == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "from and to required")
		return
	}
	if sig.ID == "" {
		sig.ID = state.SignalID()
	}
	if err := a.Store.SendSignal(r.Context(), &sig); err != nil {
		WriteError(w, http.StatusInternalServerError, "send_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sig)
}

// ListInbox returns signals delivered to `?to=`.
func (a OrchestrationAPI) ListInbox(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	project := r.URL.Query().Get("project")
	if to == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "to query param required")
		return
	}
	out, err := a.Store.ListInbox(r.Context(), project, to)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": out})
}

// G20: Checkpoints + Sentinels.

func (a OrchestrationAPI) CreateCheckpoint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID            string `json:"id"`
		Project       string `json:"project"`
		ConditionExpr string `json:"conditionExpr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.ID == "" {
		body.ID = "cp-" + randHex(8)
	}
	if err := a.Store.CreateCheckpoint(r.Context(), body.Project, body.ID, body.ConditionExpr); err != nil {
		WriteError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"checkpointId": body.ID})
}

func (a OrchestrationAPI) SatisfyCheckpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.Store.SatisfyCheckpoint(r.Context(), id); err != nil {
		WriteError(w, http.StatusInternalServerError, "satisfy_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"satisfied": true})
}

func (a OrchestrationAPI) CreateSentinel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID           string `json:"id"`
		Project      string `json:"project"`
		EventPattern string `json:"eventPattern"`
		HandlerURL   string `json:"handlerUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.ID == "" {
		body.ID = "snt-" + randHex(8)
	}
	if err := a.Store.CreateSentinel(r.Context(), body.Project, body.ID, body.EventPattern, body.HandlerURL); err != nil {
		WriteError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sentinelId": body.ID})
}

func (a OrchestrationAPI) TriggerSentinel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	handlerURL, err := a.Store.TriggerSentinel(r.Context(), id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "trigger_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggered": true, "handlerUrl": handlerURL})
}

// G21: Sketches + Crystallization.

func (a OrchestrationAPI) CreateSketch(w http.ResponseWriter, r *http.Request) {
	var sketch state.SketchRow
	if err := json.NewDecoder(r.Body).Decode(&sketch); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if sketch.ID == "" {
		sketch.ID = "sk-" + randHex(8)
	}
	if err := a.Store.CreateSketch(r.Context(), &sketch); err != nil {
		WriteError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sketchId": sketch.ID})
}

func (a OrchestrationAPI) PromoteSketch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project := r.URL.Query().Get("project")
	routine, err := a.Store.PromoteSketch(r.Context(), project, id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "promote_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, routine)
}

func (a OrchestrationAPI) Crystallize(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string   `json:"project"`
		ActionIDs []string `json:"actionIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	action, err := a.Store.Crystallize(r.Context(), body.Project, body.ActionIDs)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "crystallize_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, action)
}

func (a OrchestrationAPI) NextAction(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	action, err := a.Store.GetNextAction(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "next_failed", err.Error())
		return
	}
	if action == nil {
		writeJSON(w, http.StatusOK, map[string]any{"action": nil, "message": "no unblocked actions"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": action})
}

// Ensure crypto/rand is imported (matches randHex in memories.go).
var _ = rand.Read

// hexFor avoids "imported and not used" if randHex moves out of memories.go.
func hexFor(b []byte) string { return hex.EncodeToString(b) }
