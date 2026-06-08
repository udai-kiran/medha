package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testTSQueryExpander is an in-process stub for unit testing Hybrid wiring.
type testTSQueryExpander struct {
	expand func(query string) ([]string, error)
}

func (e *testTSQueryExpander) Expand(_ context.Context, query string) ([]string, error) {
	return e.expand(query)
}

func TestPythonTSQueryExpander_Roundtrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tsquery" || r.Method != http.MethodPost {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		var req tsqExpandRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Query == "" {
			http.Error(w, "empty query", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(tsqExpandResponse{
			Queries: []string{"jwt & authentication", "token & validation"},
		})
	}))
	defer srv.Close()

	expander := &PythonTSQueryExpander{BaseURL: srv.URL}
	queries, err := expander.Expand(context.Background(), "JWT authentication token validation")
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %d: %v", len(queries), queries)
	}
	if queries[0] != "jwt & authentication" {
		t.Errorf("q[0] = %q", queries[0])
	}
	if queries[1] != "token & validation" {
		t.Errorf("q[1] = %q", queries[1])
	}
}

func TestPythonTSQueryExpander_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	expander := &PythonTSQueryExpander{BaseURL: srv.URL}
	queries, err := expander.Expand(context.Background(), "query")
	if err == nil {
		t.Error("expected error from 500 response")
	}
	if queries != nil {
		t.Errorf("expected nil queries on error, got %v", queries)
	}
}

func TestPythonTSQueryExpander_Empty(t *testing.T) {
	expander := &PythonTSQueryExpander{BaseURL: "http://localhost:9"}
	// Empty query returns nil, nil without hitting the network.
	queries, err := expander.Expand(context.Background(), "")
	if err != nil || queries != nil {
		t.Errorf("empty query: queries=%v err=%v", queries, err)
	}
}

func TestBuildCombinedTSQuery(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"jwt & auth"}, "(jwt & auth)"},
		{[]string{"jwt & auth", "token | bearer"}, "(jwt & auth) | (token | bearer)"},
		{[]string{"", "jwt"}, "(jwt)"},
		{nil, ""},
	}
	for _, c := range cases {
		got := buildCombinedTSQuery(c.in)
		if got != c.want {
			t.Errorf("buildCombinedTSQuery(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHybrid_WithTSQueryExpander(t *testing.T) {
	store := openStore(t)
	fts, err := NewPgFTS(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-1", "p", "JWT authentication middleware")
	_ = fts.Index(ctx, "obs-2", "p", "database connection pool exhaustion")

	expanderCalled := false
	expander := &testTSQueryExpander{
		expand: func(q string) ([]string, error) {
			expanderCalled = true
			return []string{"jwt & authentication"}, nil
		},
	}

	h := &Hybrid{FTS: fts, QueryExpander: expander, K: 60}
	hits, err := h.Search(ctx, "p", "JWT authentication", "hybrid", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !expanderCalled {
		t.Error("QueryExpander.Expand was not called during hybrid search")
	}
	if len(hits) == 0 {
		t.Fatal("expected hits from expanded query")
	}
	if hits[0].ID != "obs-1" {
		t.Errorf("top hit = %q, want obs-1", hits[0].ID)
	}
}

func TestHybrid_ExpanderFallback(t *testing.T) {
	// When the expander fails, should fall back to websearch_to_tsquery.
	store := openStore(t)
	fts, err := NewPgFTS(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-1", "p", "JWT authentication middleware")

	expander := &testTSQueryExpander{
		expand: func(q string) ([]string, error) {
			return nil, fmt.Errorf("expander unavailable")
		},
	}

	h := &Hybrid{FTS: fts, QueryExpander: expander, K: 60}
	hits, err := h.Search(ctx, "p", "JWT authentication", "hybrid", 10)
	if err != nil {
		t.Fatal(err)
	}
	// Should still get hits via the websearch_to_tsquery fallback.
	if len(hits) == 0 {
		t.Error("expected fallback hits when expander fails")
	}
}
