package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// RerankCandidate pairs a hit ID with its document text for the reranker.
type RerankCandidate struct {
	ID   string
	Text string
}

// Reranker reorders a candidate list given a query. Implementations must be
// safe for concurrent use. A nil Reranker is always legal — the caller falls
// back to the pre-rerank order (RRF score).
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []RerankCandidate) ([]Hit, error)
}

// PythonReranker calls POST /rerank on the Python sidecar, which proxies
// Cohere rerank-4-fast via Bifrost. On any network or decoding error it
// returns (nil, err) so the caller can degrade gracefully.
type PythonReranker struct {
	BaseURL string
	HTTP    *http.Client
	// TopK is forwarded to the Python service. 0 means "rank all candidates".
	TopK int
	// Model overrides the default on the Python side (e.g. "cohere/rerank-4-fast").
	Model string
}

type pyRerankRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	DocIDs    []string `json:"doc_ids"`
	TopK      int      `json:"top_k,omitempty"`
	Model     string   `json:"model,omitempty"`
}

type pyRerankResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type pyRerankResponse struct {
	Results []pyRerankResult `json:"results"`
}

// Rerank sends candidates to Python /rerank and returns hits sorted by
// relevance score descending. Returns (nil, err) on any failure.
func (r *PythonReranker) Rerank(ctx context.Context, query string, candidates []RerankCandidate) ([]Hit, error) {
	if r == nil || r.BaseURL == "" || len(candidates) == 0 {
		return nil, nil
	}
	docs := make([]string, len(candidates))
	ids := make([]string, len(candidates))
	for i, c := range candidates {
		docs[i] = c.Text
		ids[i] = c.ID
	}
	payload, err := json.Marshal(pyRerankRequest{
		Query:     query,
		Documents: docs,
		DocIDs:    ids,
		TopK:      r.TopK,
		Model:     r.Model,
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(r.BaseURL, "/") + "/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rerank: HTTP %d: %s", resp.StatusCode, raw)
	}
	var out pyRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	hits := make([]Hit, len(out.Results))
	for i, res := range out.Results {
		hits[i] = Hit{ID: res.ID, Score: res.Score}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits, nil
}

// Compile-time assertion.
var _ Reranker = (*PythonReranker)(nil)
