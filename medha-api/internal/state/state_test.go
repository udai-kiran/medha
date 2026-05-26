package state

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrate_Idempotent(t *testing.T) {
	s := openTest(t)
	v1 := s.SchemaVersion
	if v1 < 1 {
		t.Fatalf("schema_version = %d, want >= 1", v1)
	}
	// Re-run migrations on the same DB; version should not advance.
	v2, err := Migrate(context.Background(), s.DB)
	if err != nil {
		t.Fatalf("Migrate again: %v", err)
	}
	if v2 != v1 {
		t.Errorf("second migrate returned %d, want %d", v2, v1)
	}
}

func TestEnsureAndGetSession(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	sess, err := s.EnsureSession(ctx, "sess-1", "demo", "/tmp/demo")
	if err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if sess.ID != "sess-1" || sess.Project != "demo" {
		t.Errorf("got %+v", sess)
	}
	// Idempotent: a second EnsureSession does not duplicate.
	if _, err := s.EnsureSession(ctx, "sess-1", "demo", "/tmp/demo"); err != nil {
		t.Fatalf("EnsureSession idempotent: %v", err)
	}

	// Missing session returns ErrNotFound.
	if _, err := s.GetSession(ctx, "sess-missing"); err != ErrNotFound {
		t.Errorf("missing got %v, want ErrNotFound", err)
	}
}

func TestInsertObservationAndCount(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.EnsureSession(ctx, "sess-1", "p", "/cwd"); err != nil {
		t.Fatal(err)
	}

	obs := &ObservationRow{
		ID: "obs-1", SessionID: "sess-1", Project: "p", HookType: "post_tool_use",
		ToolName: "read", ToolInputJSON: `{"file_path":"/x.go"}`,
		ToolOutput: "...", RawJSON: `{}`, Modality: "text",
	}
	if err := s.InsertRawObservation(ctx, obs); err != nil {
		t.Fatalf("InsertRawObservation: %v", err)
	}
	if err := s.IncrementSessionObservationCount(ctx, "sess-1"); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	got, err := s.GetObservation(ctx, "obs-1")
	if err != nil {
		t.Fatalf("GetObservation: %v", err)
	}
	if got.ToolName != "read" || got.Compressed {
		t.Errorf("got %+v", got)
	}

	sess, _ := s.GetSession(ctx, "sess-1")
	if sess.ObservationCount != 1 {
		t.Errorf("ObservationCount = %d, want 1", sess.ObservationCount)
	}
}

func TestUpdateCompressedFields(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.EnsureSession(ctx, "sess-1", "p", "")
	_ = s.InsertRawObservation(ctx, &ObservationRow{
		ID: "obs-1", SessionID: "sess-1", HookType: "post_tool_use",
		RawJSON: `{}`, Modality: "text",
	})

	upd := &ObservationRow{
		Type: "file_read", Title: "Read x.go", Subtitle: "src/x.go",
		FactsJSON: `["uses jose"]`, Narrative: "Read source file",
		ConceptsJSON: `["auth"]`, FilesJSON: `["src/x.go"]`,
		Importance: 7, Confidence: 0.8,
	}
	if err := s.UpdateCompressedFields(ctx, "obs-1", upd); err != nil {
		t.Fatalf("UpdateCompressedFields: %v", err)
	}
	got, _ := s.GetObservation(ctx, "obs-1")
	if !got.Compressed || got.Type != "file_read" || got.Importance != 7 {
		t.Errorf("got %+v", got)
	}
	if got.CompressedAt == nil {
		t.Error("CompressedAt should be set after update")
	}
}

func TestKV_PutGetDelete(t *testing.T) {
	s := openTest(t)
	kv := NewKV(s)
	ctx := context.Background()

	type payload struct {
		N int    `json:"n"`
		S string `json:"s"`
	}
	if err := kv.Put(ctx, ScopeMemories, Key(ScopeMemories, "proj", "mem-1"), payload{N: 7, S: "x"}); err != nil {
		t.Fatal(err)
	}

	var out payload
	if err := kv.Get(ctx, ScopeMemories, Key(ScopeMemories, "proj", "mem-1"), &out); err != nil {
		t.Fatal(err)
	}
	if out.N != 7 || out.S != "x" {
		t.Errorf("got %+v", out)
	}

	if err := kv.Delete(ctx, ScopeMemories, Key(ScopeMemories, "proj", "mem-1")); err != nil {
		t.Fatal(err)
	}
	if err := kv.Get(ctx, ScopeMemories, Key(ScopeMemories, "proj", "mem-1"), &out); err != ErrNotFound {
		t.Errorf("after delete got %v, want ErrNotFound", err)
	}
}

func TestKV_ListByPrefix(t *testing.T) {
	s := openTest(t)
	kv := NewKV(s)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		_ = kv.Put(ctx, ScopeFlags, Key(ScopeFlags, "proj", id), map[string]int{"v": 1})
	}
	_ = kv.Put(ctx, ScopeFlags, Key(ScopeFlags, "other", "a"), map[string]int{"v": 2})

	got, err := kv.ListByPrefix(ctx, ScopeFlags, "flags:proj:")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("ListByPrefix returned %d, want 3", len(got))
	}
}

func TestKey_Namespacing(t *testing.T) {
	cases := []struct {
		scope    Scope
		project  string
		id       string
		expected string
	}{
		{ScopeSessions, "proj", "sess-1", "sessions:proj:sess-1"},
		{ScopeAudit, "", "a-1", "audit:a-1"},
		{ScopeMemories, "proj", "", "memories:proj"},
	}
	for _, c := range cases {
		got := Key(c.scope, c.project, c.id)
		if got != c.expected {
			t.Errorf("Key(%q,%q,%q) = %q, want %q", c.scope, c.project, c.id, got, c.expected)
		}
	}
}

func TestWAL_ConcurrentReadsDontBlockWriter(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.EnsureSession(ctx, "sess-1", "p", "")
	// One writer + several readers in flight; succeed within timeout.
	deadline := time.After(2 * time.Second)
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 4; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.GetSession(ctx, "sess-1")
			}
		}()
	}
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			_ = s.IncrementSessionObservationCount(ctx, "sess-1")
		}
	}()
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// ok
	case <-deadline:
		t.Fatal("readers/writer did not finish within deadline (WAL likely off)")
	}
}
