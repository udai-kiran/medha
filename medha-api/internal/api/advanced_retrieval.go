package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// AdvancedRetrievalAPI covers facets (G26), lessons (G27), and skills (G28).
type AdvancedRetrievalAPI struct{ Store *state.Store }

func (a AdvancedRetrievalAPI) Register(r chi.Router) {
	// Facets.
	r.Post("/memories/{id}/facets", a.AddFacet)
	r.Post("/facets/query", a.QueryFacets)

	// Lessons.
	r.Post("/lessons", a.AddLesson)
	r.Get("/lessons", a.SearchLessons)

	// Skills.
	r.Post("/skills", a.UpsertSkill)
	r.Get("/skills", a.SearchSkills)
}

func (a AdvancedRetrievalAPI) AddFacet(w http.ResponseWriter, r *http.Request) {
	memoryID := chi.URLParam(r, "id")
	var body struct {
		Dimension string `json:"dimension"`
		Value     string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.Store.AddFacet(r.Context(), memoryID, body.Dimension, body.Value); err != nil {
		WriteError(w, http.StatusInternalServerError, "add_facet_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tagged": true})
}

func (a AdvancedRetrievalAPI) QueryFacets(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string              `json:"project"`
		Facets  map[string][]string `json:"facets"`
		Limit   int                 `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	mems, err := a.Store.QueryFacets(r.Context(), body.Project, body.Facets, body.Limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": mems})
}

func (a AdvancedRetrievalAPI) AddLesson(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string `json:"project"`
		SessionID string `json:"sessionId"`
		Lesson    string `json:"lesson"`
		Context   string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Lesson == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "lesson is required")
		return
	}
	row, err := a.Store.AddLesson(r.Context(), body.Project, body.SessionID, body.Lesson, body.Context)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "add_lesson_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (a AdvancedRetrievalAPI) SearchLessons(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	topic := r.URL.Query().Get("topic")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.SearchLessons(r.Context(), project, topic, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lessons": rows})
}

func (a AdvancedRetrievalAPI) UpsertSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string `json:"project"`
		SkillName string `json:"skillName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.SkillName == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "skillName is required")
		return
	}
	row, err := a.Store.UpsertSkill(r.Context(), body.Project, body.SkillName)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "upsert_skill_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a AdvancedRetrievalAPI) SearchSkills(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	skill := r.URL.Query().Get("skill")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.SearchSkills(r.Context(), project, skill, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": rows})
}
