package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// EntitiesAPI handles entity deduplication, enrichment, geocoding (G12–G14),
// and user management (G15).
type EntitiesAPI struct{ Store *state.Store }

func (a EntitiesAPI) Register(r chi.Router) {
	// Dedup (G12).
	r.Get("/entities/duplicates", a.ListDuplicates)
	r.Post("/entities/{id}/review-duplicate", a.ReviewDuplicate)

	// Enrichment (G13).
	r.Get("/entities/{id}/enrichment", a.GetEnrichment)
	r.Post("/entities/{id}/enrichment", a.SetEnrichment)

	// Geocoding (G14).
	r.Post("/entities/{id}/geocode", a.SetGeocode)
	r.Get("/entities/locations/near", a.LocationsNear)

	// Users (G15).
	r.Post("/users", a.EnsureUser)
	r.Get("/users/{identifier}", a.GetUser)
}

func (a EntitiesAPI) ListDuplicates(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	minConf, _ := strconv.ParseFloat(r.URL.Query().Get("minConfidence"), 64)
	if minConf <= 0 {
		minConf = 0.7
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := a.Store.FindPotentialDuplicates(r.Context(), project, minConf, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicates": rows})
}

func (a EntitiesAPI) ReviewDuplicate(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "id")
	var body struct {
		TargetID string `json:"targetId"`
		Confirm  bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.Store.ReviewDuplicate(r.Context(), sourceID, body.TargetID, body.Confirm); err != nil {
		WriteError(w, http.StatusInternalServerError, "review_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviewed": true})
}

func (a EntitiesAPI) GetEnrichment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	data, err := a.Store.GetEnrichment(r.Context(), id)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "entity not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (a EntitiesAPI) SetEnrichment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var data state.EnrichmentData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.Store.SetEnrichment(r.Context(), id, &data); err != nil {
		WriteError(w, http.StatusInternalServerError, "set_enrichment_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}

func (a EntitiesAPI) SetGeocode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := a.Store.SetGeocode(r.Context(), id, body.Latitude, body.Longitude); err != nil {
		WriteError(w, http.StatusInternalServerError, "geocode_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"geocoded": true})
}

func (a EntitiesAPI) LocationsNear(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	lat, _ := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, _ := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	radius, _ := strconv.ParseFloat(r.URL.Query().Get("radiusKm"), 64)
	if radius <= 0 {
		radius = 5.0
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	results, err := a.Store.SearchLocationsNear(r.Context(), project, lat, lon, radius, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": results})
}

func (a EntitiesAPI) EnsureUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identifier  string `json:"identifier"`
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Identifier == "" {
		WriteError(w, http.StatusBadRequest, "missing_param", "identifier is required")
		return
	}
	user, err := a.Store.EnsureUser(r.Context(), body.Identifier, body.DisplayName)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "ensure_user_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (a EntitiesAPI) GetUser(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "identifier")
	user, err := a.Store.GetUser(r.Context(), identifier)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}
