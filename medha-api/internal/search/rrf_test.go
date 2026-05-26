package search

import (
	"context"
	"testing"
)

func TestRRFFuse_FavoursDocumentsInMultipleLists(t *testing.T) {
	bm25 := []Hit{{ID: "a", Score: 5}, {ID: "b", Score: 3}, {ID: "c", Score: 1}}
	vec := []Hit{{ID: "b", Score: 0.9}, {ID: "d", Score: 0.8}, {ID: "a", Score: 0.7}}
	graph := []Hit{{ID: "b", Score: 1}, {ID: "e", Score: 0.5}}

	fused := RRFFuse(60, bm25, vec, graph)
	if len(fused) == 0 {
		t.Fatal("no fused output")
	}
	// b appears in all three lists, should top.
	if fused[0].ID != "b" {
		t.Errorf("top = %q, want b; full = %+v", fused[0].ID, fused)
	}
	// Sanity: descending order.
	for i := 1; i < len(fused); i++ {
		if fused[i].Score > fused[i-1].Score {
			t.Errorf("fused not descending: %+v", fused)
		}
	}
}

func TestRRFFuse_SingleListPreservesOrder(t *testing.T) {
	list := []Hit{{ID: "a", Score: 9}, {ID: "b", Score: 5}, {ID: "c", Score: 1}}
	fused := RRFFuse(60, list)
	for i, h := range list {
		if fused[i].ID != h.ID {
			t.Errorf("position %d: fused %q vs original %q", i, fused[i].ID, h.ID)
		}
	}
}

func TestDiversityBoost_CapsPerGroup(t *testing.T) {
	hits := []Hit{
		{ID: "s1-1"}, {ID: "s1-2"}, {ID: "s1-3"},
		{ID: "s2-1"}, {ID: "s2-2"}, {ID: "s3-1"},
	}
	out := DiversityBoost(hits, 2, func(id string) string {
		// session group: prefix before "-"
		for i := range id {
			if id[i] == '-' {
				return id[:i]
			}
		}
		return id
	})
	// Should keep s1-1, s1-2, s2-1, s2-2, s3-1 (drop s1-3).
	if len(out) != 5 {
		t.Fatalf("len = %d, want 5; got %+v", len(out), out)
	}
	for _, h := range out {
		if h.ID == "s1-3" {
			t.Errorf("s1-3 should have been dropped: %+v", out)
		}
	}
}

func TestHybrid_FallbackOnSingleEngine(t *testing.T) {
	// With only BM25 wired, mode=hybrid should still produce results.
	store := openStore(t)
	b, _ := NewBM25(context.Background(), store)
	_ = b.Index(context.Background(), "obs-1", "p", "JWT authentication")
	h := &Hybrid{BM25: b, K: 60}
	hits, err := h.Search(context.Background(), "p", "JWT", "hybrid", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Errorf("expected hits from single-engine hybrid; got %+v", hits)
	}
}

func TestHybrid_SingleModePassthrough(t *testing.T) {
	store := openStore(t)
	b, _ := NewBM25(context.Background(), store)
	_ = b.Index(context.Background(), "obs-1", "p", "JWT")
	h := &Hybrid{BM25: b}
	hits, _ := h.Search(context.Background(), "p", "JWT", ModeBM25, 10)
	if len(hits) != 1 || hits[0].ID != "obs-1" {
		t.Errorf("bm25 passthrough = %+v", hits)
	}
}
