package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// PatternsAPI exposes GET /patterns — recurring pattern detection for a project.
type PatternsAPI struct {
	Store *state.Store
}

func (a PatternsAPI) Register(r chi.Router) {
	r.Get("/patterns", a.List)
	r.Post("/patterns/detect", a.Detect)
}

// List returns saved patterns for the project.
func (a PatternsAPI) List(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	rows, err := a.Store.ListPatterns(r.Context(), project, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"patterns": rows})
}

// Detect runs detection and returns fresh results.
func (a PatternsAPI) Detect(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	rows, err := a.Store.DetectAndSavePatterns(r.Context(), project, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "detect_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"patterns": rows})
}
