package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/consolidation"
	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/testutil"
)

// TestE2E_CaptureCompressSearchConsolidate walks the entire happy path:
//   1. POST /observe captures three observations.
//   2. The internal compression callback (Python's role) lands them.
//   3. /smart-search returns them ranked by relevance.
//   4. /session/end triggers consolidation; memories appear in /memories.
//
// Python is faked via a httptest server so this test runs in CI without
// network dependencies.
func TestE2E_CaptureCompressSearchConsolidate(t *testing.T) {
	store := testutil.OpenStore(t)

	fts, err := search.NewPgFTS(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	graphIdx := search.NewGraphIndex(store)

	// Unique project and session so shared-DB test runs don't contaminate each other.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	project := "e2e-" + suffix
	sessID := "sess-e2e-" + suffix

	// Fake Python: synthesise summaries from inbound digests.
	pyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/summarize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sessionId": sessID, "title": "Implemented JWT auth",
				"narrative": "Added jose-based JWT middleware.",
				"keyDecisions": []string{"Use jose for JWT validation"},
				"filesModified": []string{"src/auth.ts"},
				"concepts": []string{"auth", "jwt"},
			})
		default:
			http.Error(w, "not implemented in fake", http.StatusNotImplemented)
		}
	}))
	defer pyServer.Close()

	hybrid := &search.Hybrid{FTS: fts, Graph: graphIdx, K: 60}
	pipeline := consolidation.NewPipeline(store, pyServer.URL, nil)

	// Wire the router with everything turned on except auth (empty secret).
	indexBus := indexBusFn(func(ctx context.Context, observationID, project, text string) error {
		return fts.Index(ctx, observationID, project, text)
	})
	deps := RouterDeps{
		Observe: ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(5 * time.Minute),
			Enqueuer:    NoOpEnqueuer{},
			Broadcaster: NoOpBroadcaster{},
			SessionEnd:  consolidation.SessionEndHandler{Pipeline: pipeline},
		},
		Search:   SearchDeps{Hybrid: hybrid, Store: store},
		IndexBus: indexBus,
	}
	h := NewRouter(config.FromEnv(), deps)

	// Phase 1: capture three observations.
	observations := []struct {
		toolInput  map[string]any
		toolOutput string
	}{
		{
			toolInput:  map[string]any{"file_path": "src/auth.ts"},
			toolOutput: "export function validateToken(req) { return jose.verify(req.token); }",
		},
		{
			toolInput:  map[string]any{"file_path": "src/server.ts"},
			toolOutput: "app.use('/api', validateToken);",
		},
		{
			toolInput:  map[string]any{"command": "npm test"},
			toolOutput: "all tests pass",
		},
	}
	obsIDs := make([]string, 0, len(observations))
	for i, o := range observations {
		body, _ := json.Marshal(map[string]any{
			"hookType":  "post_tool_use",
			"sessionId": sessID,
			"project":   project,
			"timestamp": time.Now().UTC().Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			"data": map[string]any{
				"tool_name":   "read",
				"tool_input":  o.toolInput,
				"tool_output": o.toolOutput,
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/agentmemory/observe", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("observe[%d] = %d body=%s", i, w.Code, w.Body.String())
		}
		var resp ObserveResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		obsIDs = append(obsIDs, resp.ObservationID)
	}

	// Phase 2: simulate Python compression by hitting the internal callback.
	for i, id := range obsIDs {
		title := "Tool call"
		files := []string{}
		if fp, ok := observations[i].toolInput["file_path"].(string); ok {
			title = "Read " + fp
			files = []string{fp}
		} else if cmd, ok := observations[i].toolInput["command"].(string); ok {
			title = "Run " + cmd
		}
		body, _ := json.Marshal(CompressedCallback{
			ID: id, SessionID: sessID,
			Type: "file_read", Title: title,
			Narrative: observations[i].toolOutput, Importance: 7, Confidence: 0.8,
			Concepts: []string{"auth", "jwt"},
			Files:    files,
		})
		req := httptest.NewRequest(http.MethodPost, "/internal/observation/"+id+"/compressed", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("compress callback[%d] = %d body=%s", i, w.Code, w.Body.String())
		}
	}

	// Phase 3: smart-search should find the JWT-related observations.
	// Use "auth jwt" so websearch_to_tsquery produces 'auth' & 'jwt', both of
	// which are present in the concept tags of the file-read observations.
	searchBody, _ := json.Marshal(map[string]any{
		"project": project, "query": "auth jwt", "mode": "bm25", "limit": 10,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/agentmemory/smart-search", bytes.NewReader(searchBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("smart-search = %d body=%s", w.Code, w.Body.String())
	}
	var searchResp SmartSearchResponse
	_ = json.Unmarshal(w.Body.Bytes(), &searchResp)
	if len(searchResp.Results) == 0 {
		t.Fatal("expected at least one search result")
	}
	// Top hit must be one of the JWT-related observations (the npm-test one is irrelevant).
	top := searchResp.Results[0].ObservationID
	if top != obsIDs[0] && top != obsIDs[1] {
		t.Errorf("top hit %q, want %q or %q", top, obsIDs[0], obsIDs[1])
	}

	// Phase 4: end the session, expect a memory to land.
	endBody, _ := json.Marshal(map[string]any{
		"hookType":  "session_end",
		"sessionId": sessID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data":      map[string]any{},
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/agentmemory/observe", bytes.NewReader(endBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("session_end = %d body=%s", w.Code, w.Body.String())
	}

	// Consolidation runs inline (NoOpSessionEndHandler does not apply here —
	// the pipeline runs synchronously before /observe returns).
	req = httptest.NewRequest(http.MethodGet, "/v1/agentmemory/memories?project="+project+"&limit=10", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("memories = %d body=%s", w2.Code, w2.Body.String())
	}
	var memsResp struct {
		Memories []map[string]any `json:"memories"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &memsResp)
	if len(memsResp.Memories) < 1 {
		t.Errorf("expected ≥1 memory after consolidation, got %d", len(memsResp.Memories))
	}
	// At least one memory should mention "JWT".
	sawJWT := false
	for _, m := range memsResp.Memories {
		title, _ := m["Title"].(string)
		if title == "" {
			title, _ = m["title"].(string)
		}
		if strings.Contains(strings.ToLower(title), "jwt") {
			sawJWT = true
		}
	}
	if !sawJWT {
		t.Errorf("no JWT-related memory: %+v", memsResp.Memories)
	}
}
