package state

import (
	"context"
	"testing"
	"time"
)

func TestInsertGetListMemory(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	m := &MemoryRow{
		ID: "mem-1", Project: "p", Type: "architecture", Tier: "semantic",
		Title: "Use jose", Content: "We use jose for JWT.",
		Concepts: []string{"auth", "jwt"}, Files: []string{"src/auth.ts"},
		SessionIDs: []string{"sess-1"}, SourceObservationIDs: []string{"obs-1"},
		Strength: 0.9,
	}
	if err := s.InsertMemory(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMemory(ctx, "mem-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Use jose" || got.Tier != "semantic" || got.Strength != 0.9 {
		t.Errorf("got %+v", got)
	}
	if len(got.Concepts) != 2 || got.Concepts[0] != "auth" {
		t.Errorf("concepts = %v", got.Concepts)
	}

	list, err := s.ListMemoriesByTier(ctx, "p", "semantic", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d", len(list))
	}

	// Wrong tier filters out.
	list, _ = s.ListMemoriesByTier(ctx, "p", "working", 10)
	if len(list) != 0 {
		t.Errorf("working tier list should be empty, got %d", len(list))
	}

	// Empty tier returns all.
	list, _ = s.ListMemoriesByTier(ctx, "p", "", 10)
	if len(list) != 1 {
		t.Errorf("all tiers list = %d", len(list))
	}
}

func TestMarkRetrieved(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_ = s.InsertMemory(ctx, &MemoryRow{ID: "mem-1", Project: "p", Title: "x", Type: "fact", Tier: "semantic"})
	if err := s.MarkRetrieved(ctx, []string{"mem-1"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMemory(ctx, "mem-1")
	if got.LastRetrievedAt == nil {
		t.Error("LastRetrievedAt not set")
	}
}

func TestUpdateMemoryStrengthAndDelete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_ = s.InsertMemory(ctx, &MemoryRow{ID: "mem-1", Project: "p", Title: "x", Type: "fact", Tier: "semantic"})
	if err := s.UpdateMemoryStrength(ctx, "mem-1", 0.3); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMemory(ctx, "mem-1")
	if got.Strength != 0.3 {
		t.Errorf("strength = %v", got.Strength)
	}
	if err := s.DeleteMemory(ctx, "mem-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMemory(ctx, "mem-1"); err != ErrNotFound {
		t.Errorf("after delete: %v", err)
	}
}

func TestEvictExpiredObservations(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.EnsureSession(ctx, "sess-1", "p", "")

	// Insert: an old raw obs (Working) + a fresh raw + an old compressed (Episodic).
	old := time.Now().UTC().Add(-48 * time.Hour)
	fresh := time.Now().UTC()
	insert := func(id string, created time.Time, compressed bool) {
		_ = s.InsertRawObservation(ctx, &ObservationRow{
			ID: id, SessionID: "sess-1", Project: "p", HookType: "post_tool_use",
			RawJSON: `{}`, Modality: "text", CreatedAt: created,
		})
		_ = compressed
	}
	insert("obs-old-raw", old, false)
	insert("obs-fresh-raw", fresh, false)
	insert("obs-old-comp", old, true)
	_ = s.UpdateCompressedFields(ctx, "obs-old-comp", &ObservationRow{
		Type: "file_read", Title: "old", Narrative: "x", Importance: 5, Confidence: 0.5,
	})

	working, episodic, err := s.EvictExpiredObservations(ctx, 24*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if working != 1 {
		t.Errorf("working evicted = %d, want 1", working)
	}
	// Episodic at 48h is well under 7d → 0 evictions.
	if episodic != 0 {
		t.Errorf("episodic evicted = %d, want 0", episodic)
	}

	// Sanity: obs-fresh-raw and obs-old-comp survive.
	if _, err := s.GetObservation(ctx, "obs-fresh-raw"); err != nil {
		t.Errorf("fresh raw should survive: %v", err)
	}
	if _, err := s.GetObservation(ctx, "obs-old-comp"); err != nil {
		t.Errorf("old compressed (within 7d) should survive: %v", err)
	}
}
