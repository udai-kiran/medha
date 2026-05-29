package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

func newFullRouter(t *testing.T) (http.Handler, *state.Store) {
	t.Helper()
	store := testutil.OpenStore(t)
	deps := RouterDeps{
		Observe: ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(time.Minute),
			Enqueuer:    NoOpEnqueuer{},
			Broadcaster: NoOpBroadcaster{},
			SessionEnd:  NoOpSessionEndHandler{Store: store},
		},
		IndexBus: NoOpIndexBus{},
	}
	return NewRouter(config.FromEnv(), deps), store
}

func post(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestSessionAPI_StartGetList(t *testing.T) {
	h, _ := newFullRouter(t)

	w := post(t, h, "/agentmemory/session/start", map[string]any{
		"sessionId": "sess-1", "project": "p", "cwd": "/tmp",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d, body=%s", w.Code, w.Body.String())
	}
	w = get(t, h, "/agentmemory/session/sess-1")
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d", w.Code)
	}
	w = get(t, h, "/agentmemory/sessions?project=p")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var body struct {
		Sessions []map[string]any `json:"sessions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(body.Sessions))
	}
}

func TestSessionAPI_GetNotFound(t *testing.T) {
	h, _ := newFullRouter(t)
	w := get(t, h, "/agentmemory/session/sess-missing")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestMemoryAPI_RememberGetListForget(t *testing.T) {
	h, _ := newFullRouter(t)

	w := post(t, h, "/agentmemory/remember", map[string]any{
		"project": "p", "type": "architecture", "title": "Use jose",
		"content": "Use jose for JWT.", "concepts": []string{"auth", "jwt"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("remember = %d, body=%s", w.Code, w.Body.String())
	}
	var rememberResp struct {
		MemoryID string `json:"memoryId"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rememberResp)
	if rememberResp.MemoryID == "" {
		t.Fatal("missing memoryId")
	}

	w = get(t, h, "/agentmemory/memory/"+rememberResp.MemoryID)
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d", w.Code)
	}

	w = get(t, h, "/agentmemory/memories?project=p")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var listResp struct {
		Memories []map[string]any `json:"memories"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Memories) != 1 {
		t.Errorf("expected 1 memory, got %d", len(listResp.Memories))
	}

	// Forget.
	w = post(t, h, "/agentmemory/forget", map[string]any{
		"memoryId": rememberResp.MemoryID, "actor": "test", "reason": "duplicate",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("forget = %d, body=%s", w.Code, w.Body.String())
	}

	// Confirm gone.
	w = get(t, h, "/agentmemory/memory/"+rememberResp.MemoryID)
	if w.Code != http.StatusNotFound {
		t.Errorf("after forget = %d, want 404", w.Code)
	}
}

func TestMemoryAPI_RememberValidation(t *testing.T) {
	h, _ := newFullRouter(t)
	w := post(t, h, "/agentmemory/remember", map[string]any{"project": "p"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing type/title status = %d", w.Code)
	}
}

func TestObservationsAPI_GetAndList(t *testing.T) {
	h, store := newFullRouter(t)
	ctx := context.Background()
	_, _ = store.EnsureSession(ctx, "sess-1", "p", "")
	for i, id := range []string{"obs-1", "obs-2"} {
		_ = store.InsertRawObservation(ctx, &state.ObservationRow{
			ID: id, SessionID: "sess-1", Project: "p", HookType: "post_tool_use",
			ToolName: "read", RawJSON: `{}`, Modality: "text",
			CreatedAt: time.Now().UTC().Add(-time.Duration(i) * time.Minute),
		})
	}

	w := get(t, h, "/agentmemory/observation/obs-1")
	if w.Code != http.StatusOK {
		t.Fatalf("get observation = %d", w.Code)
	}

	w = get(t, h, "/agentmemory/observations?session=sess-1")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var resp struct {
		Observations []map[string]any `json:"observations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Observations) != 2 {
		t.Errorf("expected 2, got %d", len(resp.Observations))
	}
}

func TestInternalAPI_PostCompressedReindexes(t *testing.T) {
	store := testutil.OpenStore(t)

	ctx := context.Background()
	_, _ = store.EnsureSession(ctx, "sess-1", "p", "")
	_ = store.InsertRawObservation(ctx, &state.ObservationRow{
		ID: "obs-1", SessionID: "sess-1", Project: "p", HookType: "post_tool_use",
		RawJSON: `{}`, Modality: "text", CreatedAt: time.Now().UTC(),
	})

	var indexedID string
	bus := indexBusFn(func(ctx context.Context, observationID, project, text string) error {
		indexedID = observationID
		return nil
	})

	deps := RouterDeps{
		Observe: ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(time.Minute),
			Enqueuer:    NoOpEnqueuer{},
			Broadcaster: NoOpBroadcaster{},
			SessionEnd:  NoOpSessionEndHandler{Store: store},
		},
		IndexBus: bus,
	}
	h := NewRouter(config.FromEnv(), deps)
	w := post(t, h, "/internal/observation/obs-1/compressed", CompressedCallback{
		ID: "obs-1", SessionID: "sess-1",
		Type: "file_read", Title: "Read auth.ts", Narrative: "JWT validation",
		Concepts: []string{"auth"}, Files: []string{"src/auth.ts"},
		Importance: 7, Confidence: 0.8,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("compressed = %d, body=%s", w.Code, w.Body.String())
	}
	if indexedID != "obs-1" {
		t.Errorf("IndexBus not called with obs-1: got %q", indexedID)
	}

	// Verify storage updated.
	row, _ := store.GetObservation(context.Background(), "obs-1")
	if !row.Compressed || row.Title != "Read auth.ts" {
		t.Errorf("row not updated: %+v", row)
	}
}

// indexBusFn lets tests pass a plain function as IndexBus.
type indexBusFn func(ctx context.Context, observationID, project, text string) error

func (f indexBusFn) IndexObservation(ctx context.Context, id, project, text string) error {
	return f(ctx, id, project, text)
}
