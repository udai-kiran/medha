package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// ProfileAPI exposes GET /profile — a snapshot of top concepts, files, and
// memory types for a project. Used for context injection and the viewer dashboard.
type ProfileAPI struct {
	Store *state.Store
}

// Register attaches the profile route.
func (a ProfileAPI) Register(r chi.Router) {
	r.Get("/profile", a.Get)
}

// Get computes and returns the project profile.
func (a ProfileAPI) Get(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")

	profile, err := a.Store.ProjectProfile(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "profile_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, profile)
}
