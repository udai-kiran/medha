package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// RecallSummaryRequest is the body for POST /v1/agentmemory/recall-summary.
// Same fields as SmartSearchRequest; limit defaults to 5 (enough signal for a summary).
type RecallSummaryRequest struct {
	Project string `json:"project,omitempty"`
	Query   string `json:"query"`
	Limit   int    `json:"limit,omitempty"`
}

// RecallSummaryResponse includes the LLM-generated summary and the raw results
// that produced it, so callers can display either or both.
type RecallSummaryResponse struct {
	Summary string              `json:"summary"`
	Query   string              `json:"query"`
	Results []SmartSearchResult `json:"results"`
}

// RecallSummaryDeps groups the dependencies for the recall-summary handler.
type RecallSummaryDeps struct {
	Search    SearchDeps
	PythonURL string
}

// RecallSummaryHandler returns a handler that fetches relevant memories via
// smart-search and condenses them into a single LLM-generated summary paragraph.
// If the Python service is unreachable, it falls back to bullet-point formatting.
func RecallSummaryHandler(deps RecallSummaryDeps) http.HandlerFunc {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req RecallSummaryRequest
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
		if req.Limit <= 0 {
			req.Limit = 5
		}

		hits, err := deps.Search.Hybrid.Search(ctx, req.Project, req.Query, "hybrid", req.Limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "search_failed", err.Error())
			return
		}

		results := make([]SmartSearchResult, 0, len(hits))
		for _, h := range hits {
			results = append(results, hydrateResult(ctx, deps.Search.Store, h))
		}

		summary := summarizeViaProxy(httpClient, deps.PythonURL, req.Query, results)

		writeJSON(w, http.StatusOK, RecallSummaryResponse{
			Summary: summary,
			Query:   req.Query,
			Results: results,
		})
	}
}

// summarizeViaProxy calls POST /summarize-memories on the Python sidecar.
// Returns a bullet-list fallback if the call fails or returns empty.
func summarizeViaProxy(client *http.Client, pythonURL, query string, results []SmartSearchResult) string {
	if pythonURL == "" || len(results) == 0 {
		return buildBulletFallback(results)
	}

	type memoryItem struct {
		Title     string  `json:"title"`
		Snippet   string  `json:"snippet"`
		Type      string  `json:"type"`
		Relevance float64 `json:"relevance"`
	}
	type pyReq struct {
		Query    string       `json:"query"`
		Memories []memoryItem `json:"memories"`
	}
	type pyResp struct {
		Summary string `json:"summary"`
	}

	items := make([]memoryItem, len(results))
	for i, r := range results {
		items[i] = memoryItem{
			Title:     r.Title,
			Snippet:   r.Snippet,
			Type:      r.Type,
			Relevance: r.Relevance,
		}
	}
	body, _ := json.Marshal(pyReq{Query: query, Memories: items})

	url := strings.TrimRight(pythonURL, "/") + "/summarize-memories"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return buildBulletFallback(results)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return buildBulletFallback(results)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return buildBulletFallback(results)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out pyResp
	if err := json.Unmarshal(raw, &out); err != nil || out.Summary == "" {
		return buildBulletFallback(results)
	}
	return out.Summary
}

// buildBulletFallback formats results as bullet points (same as the old hook logic).
func buildBulletFallback(results []SmartSearchResult) string {
	var sb strings.Builder
	for _, r := range results {
		if r.Title == "" {
			continue
		}
		sb.WriteString("• ")
		sb.WriteString(r.Title)
		if r.Snippet != "" {
			sb.WriteString(": ")
			sb.WriteString(r.Snippet)
		}
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}
