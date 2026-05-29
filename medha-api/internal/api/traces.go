package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// TracesAPI handles G11 — reasoning trace memory.
type TracesAPI struct{ Store *state.Store }

func (a TracesAPI) Register(r chi.Router) {
	r.Post("/traces", a.Start)
	r.Post("/traces/{id}/steps", a.RecordStep)
	r.Post("/traces/{id}/complete", a.Complete)
	r.Get("/traces/{id}", a.Get)
	r.Get("/traces", a.Search)
}

func (a TracesAPI) Start(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string         `json:"sessionId"`
		Project   string         `json:"project"`
		Task      string         `json:"task"`
		Metadata  map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.SessionID == "" || body.Task == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "sessionId and task are required")
		return
	}
	trace, err := a.Store.StartTrace(r.Context(), body.SessionID, body.Project, body.Task, body.Metadata)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "start_trace_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, trace)
}

func (a TracesAPI) RecordStep(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "id")
	var body struct {
		Thought     string         `json:"thought"`
		Action      string         `json:"action"`
		Observation string         `json:"observation"`
		ToolCall    *struct {
			ToolName        string         `json:"toolName"`
			Arguments       map[string]any `json:"arguments"`
			Result          map[string]any `json:"result"`
			Status          string         `json:"status"`
			ErrorMsg        string         `json:"error"`
			ExecutionTimeMs float64        `json:"executionTimeMs"`
		} `json:"toolCall"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	step, err := a.Store.RecordStep(r.Context(), traceID, body.Thought, body.Action, body.Observation)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "record_step_failed", err.Error())
		return
	}
	if body.ToolCall != nil {
		tc := body.ToolCall
		tcRow, err := a.Store.RecordToolCall(r.Context(), step.ID, tc.ToolName, tc.Arguments, tc.Result, tc.Status, tc.ErrorMsg, tc.ExecutionTimeMs)
		if err == nil {
			step.ToolCalls = append(step.ToolCalls, tcRow)
		}
	}
	writeJSON(w, http.StatusCreated, step)
}

func (a TracesAPI) Complete(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "id")
	var body struct {
		Outcome string `json:"outcome"`
		Success bool   `json:"success"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.Store.CompleteTrace(r.Context(), traceID, body.Outcome, body.Success); err != nil {
		WriteError(w, http.StatusInternalServerError, "complete_trace_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"completed": true})
}

func (a TracesAPI) Get(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "id")
	trace, err := a.Store.GetTrace(r.Context(), traceID)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "trace not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (a TracesAPI) Search(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	query := r.URL.Query().Get("query")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	traces, err := a.Store.SearchTraces(r.Context(), project, query, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces})
}
