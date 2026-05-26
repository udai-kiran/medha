package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// ObservationsAPI groups read-only handlers for /observations and
// /observation/{id}. The write path is /observe (Task 8) which has different
// validation / privacy concerns.
type ObservationsAPI struct {
	Store *state.Store
}

// Register attaches the observation read routes.
func (a ObservationsAPI) Register(r chi.Router) {
	r.Get("/observations", a.List)
	r.Get("/observation/{id}", a.Get)
}

// Get returns a single observation by id.
func (a ObservationsAPI) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := a.Store.GetObservation(r.Context(), id)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "observation not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// List returns observations filtered by session (recommended) or project.
func (a ObservationsAPI) List(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	project := r.URL.Query().Get("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	rows, err := a.Store.DB.QueryContext(r.Context(), `
        SELECT id, session_id, COALESCE(project,''), hook_type, COALESCE(tool_name,''),
               COALESCE(type,''), COALESCE(title,''), modality, has_secrets, compressed,
               created_at
        FROM observations
        WHERE (? = '' OR session_id = ?)
        AND (? = '' OR project = ?)
        ORDER BY created_at DESC LIMIT ?
    `, session, session, project, project, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	var out []map[string]any
	for rows.Next() {
		var (
			id, sess, proj, hook, tool, typ, title, modality, created string
			hasSecrets, compressed                                    int
		)
		if err := rows.Scan(&id, &sess, &proj, &hook, &tool, &typ, &title, &modality, &hasSecrets, &compressed, &created); err != nil {
			WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
			return
		}
		out = append(out, map[string]any{
			"id": id, "sessionId": sess, "project": proj,
			"hookType": hook, "toolName": tool,
			"type": typ, "title": title,
			"modality":   modality,
			"hasSecrets": hasSecrets != 0,
			"compressed": compressed != 0,
			"createdAt":  created,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"observations": out})
}
