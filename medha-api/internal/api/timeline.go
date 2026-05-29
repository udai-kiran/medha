package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// TimelineAPI exposes GET /timeline — chronological observations with pagination.
type TimelineAPI struct {
	Store *state.Store
}

func (a TimelineAPI) Register(r chi.Router) {
	r.Get("/timeline", a.List)
}

// List returns observations in chronological order with cursor-based pagination.
// Query params: project, session, hookType, filePath, after (ISO timestamp cursor),
// before, limit (default 50).
func (a TimelineAPI) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := q.Get("project")
	session := q.Get("session")
	hookType := q.Get("hookType")
	after := q.Get("after")
	before := q.Get("before")
	filePath := q.Get("filePath")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	entries, err := a.Store.Timeline(r.Context(), state.TimelineFilter{
		Project:  project,
		Session:  session,
		HookType: hookType,
		FilePath: filePath,
		After:    after,
		Before:   before,
		Limit:    limit,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "timeline_failed", err.Error())
		return
	}

	var nextCursor string
	if len(entries) == limit {
		nextCursor = entries[len(entries)-1].CreatedAt
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timeline":   entries,
		"nextCursor": nextCursor,
	})
}
