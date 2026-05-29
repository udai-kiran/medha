package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// ObservationsAPI groups read-only handlers for /observations, /observation/{id},
// and /file-history.
type ObservationsAPI struct {
	Store *state.Store
}

// Register attaches the observation read routes.
func (a ObservationsAPI) Register(r chi.Router) {
	r.Get("/observations", a.List)
	r.Get("/observation/{id}", a.Get)
	r.Get("/file-history", a.FileHistory)
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
        WHERE ($1 = '' OR session_id = $1)
        AND ($2 = '' OR project = $2)
        ORDER BY created_at DESC LIMIT $3
    `, session, project, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	var out []map[string]any
	for rows.Next() {
		var (
			id, sess, proj, hook, tool, typ, title, modality, created string
			hasSecrets, compressed                                     int
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

// FileHistory returns the chronological list of compressed observations that
// touched a given file path. Query params: project (optional), filePath (required), limit.
func (a ObservationsAPI) FileHistory(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	filePath := r.URL.Query().Get("filePath")
	if filePath == "" {
		WriteError(w, http.StatusBadRequest, "missing_param", "filePath is required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	entries, err := a.Store.FileHistory(r.Context(), project, filePath, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "file_history_failed", err.Error())
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id": e.ID, "sessionId": e.SessionID, "project": e.Project,
			"hookType": e.HookType, "toolName": e.ToolName,
			"type": e.Type, "title": e.Title, "createdAt": e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"filePath": filePath, "history": out})
}
