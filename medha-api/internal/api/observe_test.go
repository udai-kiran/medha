package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/state"
)

func mustOpenStore(t *testing.T) *state.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "observe-test.db")
	s, err := state.Open(context.Background(), state.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

type spyEnqueuer struct{ n atomic.Int64 }

func (s *spyEnqueuer) EnqueueCompress(ctx context.Context, observationID, sessionID string) error {
	s.n.Add(1)
	return nil
}

type spyBroadcaster struct{ n atomic.Int64 }

func (s *spyBroadcaster) BroadcastObservation(ctx context.Context, sessionID, observationID, event string) {
	s.n.Add(1)
}

func newRouter(t *testing.T) (http.Handler, *state.Store, *spyEnqueuer, *spyBroadcaster) {
	t.Helper()
	store := mustOpenStore(t)
	enq := &spyEnqueuer{}
	br := &spyBroadcaster{}
	deps := RouterDeps{
		Observe: ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(5 * time.Minute),
			Enqueuer:    enq,
			Broadcaster: br,
			SessionEnd:  NoOpSessionEndHandler{Store: store},
		},
	}
	cfg := config.FromEnv()
	return NewRouter(cfg, deps), store, enq, br
}

func postObserve(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/agentmemory/observe", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestObserve_ValidCreatesRow(t *testing.T) {
	h, store, enq, br := newRouter(t)
	payload := map[string]any{
		"hookType":  "post_tool_use",
		"sessionId": "sess-1",
		"project":   "proj",
		"cwd":       "/tmp/proj",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{
			"tool_name":   "read",
			"tool_input":  map[string]any{"file_path": "/x.go"},
			"tool_output": "package main",
		},
	}
	w := postObserve(t, h, payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp ObserveResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ObservationID == "" || !resp.Compressing || resp.Compressed {
		t.Errorf("resp = %+v", resp)
	}

	row, err := store.GetObservation(context.Background(), resp.ObservationID)
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if row.ToolName != "read" || row.SessionID != "sess-1" {
		t.Errorf("stored row = %+v", row)
	}
	if enq.n.Load() != 1 {
		t.Errorf("expected 1 enqueue, got %d", enq.n.Load())
	}
	if br.n.Load() != 1 {
		t.Errorf("expected 1 broadcast, got %d", br.n.Load())
	}
}

func TestObserve_MalformedPayloadIs400(t *testing.T) {
	h, _, _, _ := newRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/agentmemory/observe", bytes.NewReader([]byte("{not-json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestObserve_MissingHookTypeIs400(t *testing.T) {
	h, _, _, _ := newRouter(t)
	w := postObserve(t, h, map[string]any{
		"sessionId": "sess-1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data":      map[string]any{},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestObserve_UnknownHookTypeIs400(t *testing.T) {
	h, _, _, _ := newRouter(t)
	w := postObserve(t, h, map[string]any{
		"hookType":  "garbage",
		"sessionId": "sess-1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data":      map[string]any{},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
}

func TestObserve_SecretNeverReachesStorage(t *testing.T) {
	// FR-NFR-10: the privacy filter runs before storage. Even if a secret is
	// inside the inbound payload, it must never appear in the stored row or
	// be enqueued.
	const secret = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789aBcDeFgHiJkLmNoPqRs"
	h, store, enq, _ := newRouter(t)
	payload := map[string]any{
		"hookType":  "post_tool_use",
		"sessionId": "sess-1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{
			"tool_name": "shell",
			"tool_input": map[string]any{
				"command": "curl -H 'Authorization: Bearer " + secret + "' https://api.example.com",
			},
			"tool_output": "ok",
		},
	}
	w := postObserve(t, h, payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp ObserveResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	row, err := store.GetObservation(context.Background(), resp.ObservationID)
	if err != nil {
		t.Fatal(err)
	}
	full := row.RawJSON + " | " + row.ToolInputJSON
	if strings.Contains(full, secret) {
		t.Errorf("secret leaked into storage: row=%s", full)
	}
	if !row.HasSecrets {
		t.Error("HasSecrets flag must be set on rows that triggered redaction")
	}
	if enq.n.Load() != 1 {
		t.Errorf("expected 1 enqueue (secret was filtered, observation still valid), got %d", enq.n.Load())
	}
}

func TestObserve_Deduplicates(t *testing.T) {
	h, _, enq, _ := newRouter(t)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	payload := map[string]any{
		"hookType":  "post_tool_use",
		"sessionId": "sess-1",
		"timestamp": ts,
		"data": map[string]any{
			"tool_name":   "read",
			"tool_input":  map[string]any{"file_path": "/x.go"},
			"tool_output": "y",
		},
	}
	w1 := postObserve(t, h, payload)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d", w1.Code)
	}
	w2 := postObserve(t, h, payload)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("duplicate POST status = %d, want 202", w2.Code)
	}
	var resp ObserveResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if !resp.Deduplicated {
		t.Error("Deduplicated flag not set on duplicate response")
	}
	// Duplicate must NOT enqueue compression a second time.
	if got := enq.n.Load(); got != 1 {
		t.Errorf("enqueue count = %d, want 1", got)
	}
}

func TestObserve_SessionEndReturns202(t *testing.T) {
	h, store, _, _ := newRouter(t)
	w := postObserve(t, h, map[string]any{
		"hookType":  "session_end",
		"sessionId": "sess-1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data":      map[string]any{"summary_hint": "done"},
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	sess, err := store.GetSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Status != "completed" {
		t.Errorf("session status = %q, want completed", sess.Status)
	}
}

func TestObserve_LatencyUnder50ms(t *testing.T) {
	// NFR-2: p99 of POST /observe under 50 ms with stubbed async deps.
	// Run many iterations and check p99 stays under budget.
	h, _, _, _ := newRouter(t)
	durations := make([]time.Duration, 200)
	for i := 0; i < len(durations); i++ {
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		payload := map[string]any{
			"hookType":  "post_tool_use",
			"sessionId": "sess-perf",
			"timestamp": ts,
			"data": map[string]any{
				"tool_name":   "shell",
				"tool_input":  map[string]any{"cmd": "ls", "n": i}, // unique each time
				"tool_output": "ok",
			},
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/agentmemory/observe", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(w, req)
		durations[i] = time.Since(start)
		if w.Code != http.StatusCreated {
			t.Fatalf("iteration %d: status %d", i, w.Code)
		}
	}
	// p99 = 99th of N=200 sorted durations.
	for i := 0; i < len(durations); i++ {
		for j := i + 1; j < len(durations); j++ {
			if durations[j] < durations[i] {
				durations[i], durations[j] = durations[j], durations[i]
			}
		}
	}
	p99 := durations[int(float64(len(durations))*0.99)-1]
	if p99 > 50*time.Millisecond {
		t.Errorf("p99 = %v exceeds 50ms budget", p99)
	}
}
