package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// IndexBus is the minimal contract the search engines satisfy for the
// internal compression callback — when Python writes back a compressed
// observation, we re-index it across BM25 / vector / graph. The actual
// search.Hybrid struct holds the engines; we expose only the side-effect.
type IndexBus interface {
	IndexObservation(ctx context.Context, observationID, project, text string) error
}

// NoOpIndexBus is the zero-value placeholder. The real implementation lives
// in cmd/api/main.go because it has direct access to the search engines.
type NoOpIndexBus struct{}

// IndexObservation does nothing.
func (NoOpIndexBus) IndexObservation(ctx context.Context, observationID, project, text string) error {
	return nil
}

// InternalAPI groups handlers under /internal — service-to-service callbacks
// the Python worker uses to report compression / extraction results.
type InternalAPI struct {
	Store    *state.Store
	IndexBus IndexBus
}

// RegisterPublic mounts the /agentmemory/* projections of internal endpoints
// (typically none — public access is via /observe + /smart-search).
func (a InternalAPI) RegisterPublic(r chi.Router) {
	// no-op for now
}

// RegisterInternal mounts the actual /internal/* callbacks.
func (a InternalAPI) RegisterInternal(r chi.Router) {
	r.Post("/observation/{id}/compressed", a.PostCompressed)
}

// CompressedCallback is the body of POST /internal/observation/{id}/compressed.
// Matches the Python `CompressedObservation` shape.
type CompressedCallback struct {
	ID               string   `json:"id"`
	SessionID        string   `json:"sessionId"`
	Type             string   `json:"type"`
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	Facts            []string `json:"facts"`
	Narrative        string   `json:"narrative"`
	Concepts         []string `json:"concepts"`
	Files            []string `json:"files"`
	Importance       int      `json:"importance"`
	Confidence       float64  `json:"confidence"`
	ImageDescription string   `json:"imageDescription,omitempty"`
}

// PostCompressed writes a compression result back to storage and triggers
// re-indexing across the search engines.
func (a InternalAPI) PostCompressed(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "id required")
		return
	}
	var cb CompressedCallback
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if cb.ID == "" {
		cb.ID = id
	}

	conceptsJSON, _ := json.Marshal(cb.Concepts)
	filesJSON, _ := json.Marshal(cb.Files)
	factsJSON, _ := json.Marshal(cb.Facts)

	if err := a.Store.UpdateCompressedFields(r.Context(), id, &state.ObservationRow{
		Type: cb.Type, Title: cb.Title, Subtitle: cb.Subtitle,
		FactsJSON: string(factsJSON), Narrative: cb.Narrative,
		ConceptsJSON: string(conceptsJSON), FilesJSON: string(filesJSON),
		Importance: cb.Importance, Confidence: cb.Confidence,
		ImageDescription: cb.ImageDescription,
	}); err != nil {
		WriteError(w, http.StatusInternalServerError, "persist_failed", err.Error())
		return
	}

	// Re-index for search.
	if a.IndexBus != nil {
		row, err := a.Store.GetObservation(r.Context(), id)
		if err == nil && row != nil {
			text := buildIndexText(row, cb)
			_ = a.IndexBus.IndexObservation(r.Context(), id, row.Project, text)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "id": id})
}

// buildIndexText assembles the searchable text for BM25 + vector indexing.
// Centralised here so both the callback and any backfill share the same shape.
func buildIndexText(_ *state.ObservationRow, cb CompressedCallback) string {
	parts := []string{cb.Title, cb.Subtitle, cb.Narrative}
	parts = append(parts, cb.Concepts...)
	parts = append(parts, cb.Files...)
	parts = append(parts, cb.Facts...)
	var s string
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i > 0 {
			s += " "
		}
		s += p
	}
	return s
}
