package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

// newRecallRouter builds a router with a real FTS engine and a fake Python
// server so the recall-summary endpoint can be exercised end-to-end.
func newRecallRouter(t *testing.T, pythonURL string) (http.Handler, *state.Store, *search.PgFTS) {
	t.Helper()
	store := testutil.OpenStore(t)
	fts, err := search.NewPgFTS(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	hybrid := &search.Hybrid{FTS: fts, K: 60}
	deps := RouterDeps{
		Observe: ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(time.Minute),
			Enqueuer:    NoOpEnqueuer{},
			Broadcaster: NoOpBroadcaster{},
			SessionEnd:  NoOpSessionEndHandler{Store: store},
		},
		Search:        SearchDeps{Hybrid: hybrid, Store: store},
		PythonBaseURL: pythonURL,
	}
	return NewRouter(config.FromEnv(), deps), store, fts
}

func TestRecallSummary_LLMSummary(t *testing.T) {
	// Fake Python sidecar returns a canned summary.
	py := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/summarize-memories" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"summary": "JWT middleware uses jose for token validation in src/auth.ts.",
		})
	}))
	defer py.Close()

	// Use nano-time suffix so IDs are unique across test runs on a shared DB.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	proj := "rcs-llm-" + suffix
	sessID := "rcs-sess-" + suffix
	obsID := "rcs-obs-" + suffix

	h, store, fts := newRecallRouter(t, py.URL)
	ctx := t.Context()
	_, _ = store.EnsureSession(ctx, sessID, proj, "/tmp")
	_ = store.InsertRawObservation(ctx, &state.ObservationRow{
		ID: obsID, SessionID: sessID, Project: proj, HookType: "post_tool_use",
		RawJSON: `{}`, Modality: "text", CreatedAt: time.Now().UTC(),
	})
	_ = store.UpdateCompressedFields(ctx, obsID, &state.ObservationRow{
		Type: "file_read", Title: "JWT middleware", Narrative: "validates tokens via jose",
		Importance: 8, Confidence: 0.9,
	})
	_ = fts.Index(ctx, obsID, proj, "JWT middleware validates tokens via jose authentication")

	body, _ := json.Marshal(RecallSummaryRequest{Project: proj, Query: "JWT authentication"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agentmemory/recall-summary", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp RecallSummaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Summary != "JWT middleware uses jose for token validation in src/auth.ts." {
		t.Errorf("summary = %q", resp.Summary)
	}
	if len(resp.Results) == 0 {
		t.Error("expected raw results alongside summary")
	}
}

func TestRecallSummary_FallbackWhenPythonDown(t *testing.T) {
	// Use nano-time suffix so IDs are unique across test runs on a shared DB.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	proj := "rcs-fb-" + suffix
	sessID := "rcs-fb-sess-" + suffix
	obsID := "rcs-fb-obs-" + suffix

	// Use a URL that immediately refuses — simulates Python being down.
	h, store, fts := newRecallRouter(t, "http://127.0.0.1:1") // port 1 = refused
	ctx := t.Context()
	_, _ = store.EnsureSession(ctx, sessID, proj, "/tmp")
	if err := store.InsertRawObservation(ctx, &state.ObservationRow{
		ID: obsID, SessionID: sessID, Project: proj, HookType: "post_tool_use",
		RawJSON: `{}`, Modality: "text", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertRawObservation: %v", err)
	}
	if err := store.UpdateCompressedFields(ctx, obsID, &state.ObservationRow{
		Type: "file_read", Title: "Rate limiting middleware",
		Narrative:    "token bucket in middleware.ts",
		FactsJSON:    `[]`,
		ConceptsJSON: `[]`,
		FilesJSON:    `[]`,
		Importance:   7, Confidence: 0.8,
	}); err != nil {
		t.Fatalf("UpdateCompressedFields: %v", err)
	}
	if err := fts.Index(ctx, obsID, proj, "middleware token bucket configuration implementation"); err != nil {
		t.Fatalf("fts.Index: %v", err)
	}

	body, _ := json.Marshal(RecallSummaryRequest{Project: proj, Query: "middleware token"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agentmemory/recall-summary", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp RecallSummaryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	// Python is down → bullet fallback contains the memory title.
	if !strings.HasPrefix(resp.Summary, "•") {
		t.Errorf("expected bullet fallback, got %q", resp.Summary)
	}
	if !strings.Contains(resp.Summary, "Rate limiting middleware") {
		t.Errorf("summary missing title: %q", resp.Summary)
	}
}

func TestRecallSummary_ValidationError(t *testing.T) {
	h, _, _ := newRecallRouter(t, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/agentmemory/recall-summary",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing query should be 400, got %d", w.Code)
	}
}

func TestBuildBulletFallback(t *testing.T) {
	results := []SmartSearchResult{
		{Title: "JWT middleware", Snippet: "validates tokens"},
		{Title: "CORS handler", Snippet: ""},
		{Title: "", Snippet: "ignored"},
	}
	got := buildBulletFallback(results)
	if !strings.Contains(got, "• JWT middleware: validates tokens") {
		t.Errorf("missing first bullet: %q", got)
	}
	if !strings.Contains(got, "• CORS handler\n") && !strings.HasSuffix(got, "• CORS handler") {
		t.Errorf("missing second bullet (no snippet): %q", got)
	}
	if strings.Contains(got, "ignored") {
		t.Errorf("empty-title entry should be skipped: %q", got)
	}
}
