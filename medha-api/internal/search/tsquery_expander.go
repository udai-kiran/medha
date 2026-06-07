package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TSQueryExpander converts a natural-language query into one or more
// optimized PostgreSQL ts_query strings via an LLM agent. The returned strings
// use to_tsquery syntax (&, |, <->, parentheses). A nil expander is always
// legal — the caller falls back to websearch_to_tsquery with the raw input.
type TSQueryExpander interface {
	Expand(ctx context.Context, query string) ([]string, error)
}

// PythonTSQueryExpander calls POST /tsquery on the Python sidecar, which uses
// an LLM (via Bifrost) to compile the query into structured ts_query strings.
// On any error it returns (nil, err) so callers can degrade to plain search.
type PythonTSQueryExpander struct {
	BaseURL string
	HTTP    *http.Client
}

type tsqExpandRequest struct {
	Query string `json:"query"`
}

type tsqExpandResponse struct {
	Queries []string `json:"queries"`
}

// Expand calls Python /tsquery and returns the compiled query list.
func (e *PythonTSQueryExpander) Expand(ctx context.Context, query string) ([]string, error) {
	if e == nil || e.BaseURL == "" || query == "" {
		return nil, nil
	}
	body, err := json.Marshal(tsqExpandRequest{Query: query})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(e.BaseURL, "/") + "/tsquery"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := e.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tsquery expand: HTTP %d: %s", resp.StatusCode, raw)
	}
	var out tsqExpandResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Queries, nil
}

// Compile-time assertion.
var _ TSQueryExpander = (*PythonTSQueryExpander)(nil)
