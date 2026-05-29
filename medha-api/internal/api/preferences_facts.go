package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// PreferencesAPI handles G09 — preference memory.
type PreferencesAPI struct{ Store *state.Store }

func (a PreferencesAPI) Register(r chi.Router) {
	r.Post("/preferences", a.Add)
	r.Get("/preferences", a.Search)
	r.Delete("/preference/{id}", a.Delete)
}

func (a PreferencesAPI) Add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project    string         `json:"project"`
		Category   string         `json:"category"`
		Preference string         `json:"preference"`
		Confidence float64        `json:"confidence"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Category == "" || body.Preference == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "category and preference are required")
		return
	}
	row, err := a.Store.AddPreference(r.Context(), body.Project, body.Category, body.Preference, body.Confidence, body.Metadata)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "add_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (a PreferencesAPI) Search(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	category := r.URL.Query().Get("category")
	query := r.URL.Query().Get("query")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.SearchPreferences(r.Context(), project, category, query, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": rows})
}

func (a PreferencesAPI) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.Store.DeletePreference(r.Context(), id); err != nil {
		WriteError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// FactsAPI handles G10 — facts memory.
type FactsAPI struct{ Store *state.Store }

func (a FactsAPI) Register(r chi.Router) {
	r.Post("/facts", a.Add)
	r.Get("/facts", a.Search)
}

func (a FactsAPI) Add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project    string  `json:"project"`
		Subject    string  `json:"subject"`
		Predicate  string  `json:"predicate"`
		ObjectVal  string  `json:"object"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Subject == "" || body.Predicate == "" || body.ObjectVal == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "subject, predicate, and object are required")
		return
	}
	row, err := a.Store.AddFact(r.Context(), body.Project, body.Subject, body.Predicate, body.ObjectVal, body.Confidence)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "add_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (a FactsAPI) Search(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	subject := r.URL.Query().Get("subject")
	predicate := r.URL.Query().Get("predicate")
	query := r.URL.Query().Get("query")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.SearchFacts(r.Context(), project, subject, predicate, query, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"facts": rows})
}
