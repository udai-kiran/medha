package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/telemetry"
)

// SearchDeps groups the dependencies the search handler needs. Wired in
// router setup; tests can supply minimal fakes.
type SearchDeps struct {
	Hybrid *search.Hybrid
	Store  *state.Store
}

// SmartSearchRequest is the body for POST /agentmemory/smart-search.
type SmartSearchRequest struct {
	Project string `json:"project,omitempty"`
	Query   string `json:"query"`
	Limit   int    `json:"limit,omitempty"`
	Mode    string `json:"mode,omitempty"` // bm25 | vector | graph | hybrid
}

// SmartSearchResult is one item in the response.
type SmartSearchResult struct {
	ObservationID string   `json:"observationId"`
	Type          string   `json:"type,omitempty"`
	Title         string   `json:"title,omitempty"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Snippet       string   `json:"snippet,omitempty"`
	Concepts      []string `json:"concepts,omitempty"`
	SessionID     string   `json:"sessionId,omitempty"`
	Timestamp     string   `json:"timestamp,omitempty"`
	Relevance     float64  `json:"relevance"`
}

// SmartSearchResponse wraps results and echoes the mode used.
type SmartSearchResponse struct {
	Mode    string              `json:"mode"`
	Query   string              `json:"query"`
	Results []SmartSearchResult `json:"results"`
}

// SmartSearchHandler returns the chi handler.
func SmartSearchHandler(deps SearchDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := telemetry.LoggerFrom(ctx)

		var req SmartSearchRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
			return
		}
		if req.Query == "" {
			WriteError(w, http.StatusBadRequest, "validation_failed", "query is required")
			return
		}
		mode := strings.ToLower(req.Mode)
		if mode == "" {
			mode = search.ModeHybrid
		}
		switch mode {
		case search.ModeBM25, search.ModeVector, search.ModeGraph, search.ModeHybrid:
		default:
			WriteError(w, http.StatusBadRequest, "invalid_mode",
				"mode must be one of: bm25, vector, graph, hybrid")
			return
		}
		if req.Limit <= 0 {
			req.Limit = 10
		}

		start := time.Now()
		hits, err := deps.Hybrid.Search(ctx, req.Project, req.Query, mode, req.Limit)
		if err != nil {
			log.Error("search.failed", "err", err)
			WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
			return
		}
		log.Info("search.done", "mode", mode, "hits", len(hits), "duration_ms", time.Since(start).Milliseconds())

		results := make([]SmartSearchResult, 0, len(hits))
		for _, h := range hits {
			r := hydrateResult(ctx, deps.Store, h)
			results = append(results, r)
		}

		writeJSON(w, http.StatusOK, SmartSearchResponse{
			Mode:    mode,
			Query:   req.Query,
			Results: results,
		})
	}
}

// hydrateResult enriches a raw Hit with row-level fields (title, snippet, ...).
// Skips silently if the row is missing (it may have been evicted by decay).
func hydrateResult(ctx context.Context, store *state.Store, h search.Hit) SmartSearchResult {
	out := SmartSearchResult{
		ObservationID: h.ID,
		Relevance:     h.Score,
	}
	if store == nil {
		return out
	}
	row, err := store.GetObservation(ctx, h.ID)
	if err != nil || row == nil {
		return out
	}
	out.Type = row.Type
	out.Title = row.Title
	out.Subtitle = row.Subtitle
	out.SessionID = row.SessionID
	if !row.CreatedAt.IsZero() {
		out.Timestamp = row.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if row.Narrative != "" {
		out.Snippet = clipSnippet(row.Narrative, 200)
	} else if row.ToolOutput != "" {
		out.Snippet = clipSnippet(row.ToolOutput, 200)
	}
	if row.ConceptsJSON != "" {
		var concepts []string
		if err := json.Unmarshal([]byte(row.ConceptsJSON), &concepts); err == nil {
			out.Concepts = concepts
		}
	}
	return out
}

func clipSnippet(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	// Try to break on a word boundary close to the cut.
	cut := s[:max]
	if idx := strings.LastIndexAny(cut, " \t\n"); idx > max/2 {
		return cut[:idx] + "…"
	}
	return cut + "…"
}
