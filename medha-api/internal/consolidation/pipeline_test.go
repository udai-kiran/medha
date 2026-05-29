package consolidation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

func openConsolStore(t *testing.T) *state.Store {
	return testutil.OpenStore(t)
}

func insertCompressed(t *testing.T, s *state.Store, id, sess string, narrative string, files []string, concepts []string) {
	t.Helper()
	ctx := context.Background()
	if err := s.InsertRawObservation(ctx, &state.ObservationRow{
		ID: id, SessionID: sess, Project: "p", HookType: "post_tool_use",
		RawJSON: `{}`, Modality: "text", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	filesJSON, _ := json.Marshal(files)
	conceptsJSON, _ := json.Marshal(concepts)
	if err := s.UpdateCompressedFields(ctx, id, &state.ObservationRow{
		Type: "file_read", Title: "Read",
		Narrative: narrative, FilesJSON: string(filesJSON), ConceptsJSON: string(conceptsJSON),
		Importance: 5, Confidence: 0.8,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPipeline_Run_WithStubbedPython(t *testing.T) {
	store := openConsolStore(t)
	ctx := context.Background()

	_, _ = store.EnsureSession(ctx, "sess-1", "p", "/tmp")
	insertCompressed(t, store, "obs-1", "sess-1",
		"Examined the JWT validation middleware. Decided to use jose over jsonwebtoken.",
		[]string{"src/auth.ts"}, []string{"auth", "jwt"})
	insertCompressed(t, store, "obs-2", "sess-1",
		"Wire JWT middleware on /api/* routes.",
		[]string{"src/server.ts"}, []string{"auth"})

	// Stub Python /summarize.
	called := false
	pyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/summarize" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sessionId":      "sess-1",
			"title":          "Implement JWT",
			"narrative":      "Added JWT validation with jose.",
			"keyDecisions":   []string{"Use jose", "1h expiry"},
			"filesModified":  []string{"src/auth.ts", "src/server.ts"},
			"concepts":       []string{"auth", "jwt"},
		})
	}))
	defer pyServer.Close()

	p := NewPipeline(store, pyServer.URL, nil)
	memCount, err := p.Run(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected /summarize to be called")
	}
	// One workflow memory + one per decision = 3.
	if memCount != 3 {
		t.Errorf("memCount = %d, want 3", memCount)
	}

	// SessionSummary persisted.
	var title string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT title FROM sessions_summary WHERE session_id = 'sess-1'`,
	).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Implement JWT" {
		t.Errorf("title = %q", title)
	}

	// Session marked completed.
	sess, _ := store.GetSession(ctx, "sess-1")
	if sess.Status != "completed" {
		t.Errorf("status = %q", sess.Status)
	}

	// Memories persisted.
	var n int
	if err := store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("memories rows = %d, want 3", n)
	}
}

func TestPipeline_FallsBackOnPythonError(t *testing.T) {
	store := openConsolStore(t)
	ctx := context.Background()
	_, _ = store.EnsureSession(ctx, "sess-1", "p", "")
	insertCompressed(t, store, "obs-1", "sess-1",
		"Read auth.ts and use jose for JWT.",
		[]string{"src/auth.ts"}, []string{"auth"})

	// Python that 500s — pipeline must fall back to the synthetic Go summary.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewPipeline(store, srv.URL, nil)
	memCount, err := p.Run(ctx, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if memCount < 1 {
		t.Error("expected at least the workflow memory even on fallback")
	}
	var title string
	if err := store.DB.QueryRowContext(ctx,
		`SELECT title FROM sessions_summary WHERE session_id = 'sess-1'`,
	).Scan(&title); err != nil {
		t.Fatal(err)
	}
	// Synthetic summary uses "Session on <concept>".
	if title == "" {
		t.Error("expected synthetic title to be persisted")
	}
}

func TestPipeline_NoOpsOnEmptySession(t *testing.T) {
	store := openConsolStore(t)
	ctx := context.Background()
	_, _ = store.EnsureSession(ctx, "sess-empty", "p", "")
	p := NewPipeline(store, "http://invalid", nil)
	n, err := p.Run(ctx, "sess-empty")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 memories on empty session, got %d", n)
	}
}

func TestSessionEndHandler_NilPipelineNoOp(t *testing.T) {
	h := SessionEndHandler{}
	if err := h.OnSessionEnd(context.Background(), "sess-1"); err != nil {
		t.Errorf("nil pipeline should no-op, got %v", err)
	}
}
