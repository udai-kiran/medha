package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
)

func newSearchRouter(t *testing.T) (http.Handler, *state.Store, *search.BM25) {
	t.Helper()
	store, err := state.Open(context.Background(), state.Options{Path: filepath.Join(t.TempDir(), "search.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	bm25, err := search.NewBM25(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	hybrid := &search.Hybrid{BM25: bm25, K: 60}

	deps := RouterDeps{
		Observe: ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(time.Minute),
			Enqueuer:    NoOpEnqueuer{},
			Broadcaster: NoOpBroadcaster{},
			SessionEnd:  NoOpSessionEndHandler{Store: store},
		},
		Search: SearchDeps{Hybrid: hybrid, Store: store},
	}
	return NewRouter(config.FromEnv(), deps), store, bm25
}

func TestSmartSearch_Roundtrip(t *testing.T) {
	h, store, bm25 := newSearchRouter(t)
	ctx := context.Background()
	_, _ = store.EnsureSession(ctx, "sess-1", "p", "/tmp")

	// Insert compressed observations and index them.
	for id, narrative := range map[string]string{
		"obs-1": "Read authentication middleware implementing JWT validation",
		"obs-2": "Configure CORS for the API gateway",
		"obs-3": "Implement JWT token refresh endpoint",
	} {
		_ = store.InsertRawObservation(ctx, &state.ObservationRow{
			ID: id, SessionID: "sess-1", Project: "p", HookType: "post_tool_use",
			RawJSON: `{}`, Modality: "text", CreatedAt: time.Now().UTC(),
		})
		_ = store.UpdateCompressedFields(ctx, id, &state.ObservationRow{
			Type: "file_read", Title: "Read", Narrative: narrative,
			Importance: 5, Confidence: 0.8,
		})
		_ = bm25.Index(ctx, id, "p", narrative)
	}

	body, _ := json.Marshal(SmartSearchRequest{Project: "p", Query: "JWT authentication", Mode: "bm25", Limit: 5})
	req := httptest.NewRequest(http.MethodPost, "/agentmemory/smart-search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp SmartSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "bm25" {
		t.Errorf("Mode = %q, want bm25", resp.Mode)
	}
	if len(resp.Results) == 0 {
		t.Fatal("no results")
	}
	if !strings.HasPrefix(resp.Results[0].ObservationID, "obs-") {
		t.Errorf("ObservationID = %q", resp.Results[0].ObservationID)
	}
	if resp.Results[0].Title == "" {
		t.Error("hydrated row should have Title")
	}
	if resp.Results[0].Snippet == "" {
		t.Error("hydrated row should have Snippet")
	}
}

func TestSmartSearch_Validation(t *testing.T) {
	h, _, _ := newSearchRouter(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing query", `{"mode":"bm25"}`, http.StatusBadRequest},
		{"bad mode", `{"query":"x","mode":"wat"}`, http.StatusBadRequest},
		{"bad json", `{not-json`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/agentmemory/smart-search", bytes.NewReader([]byte(c.body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != c.want {
				t.Errorf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}

func TestClipSnippet(t *testing.T) {
	got := clipSnippet("hello world foo bar baz qux", 12)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("missing ellipsis: %q", got)
	}
	if got := clipSnippet("short", 100); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
}
