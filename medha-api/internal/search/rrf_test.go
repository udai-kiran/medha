package search

import (
	"context"
	"testing"
	"time"
)

func TestRecencyBoost(t *testing.T) {
	const w, half = 0.5, 7.0
	// Disabled when weight is 0.
	if got := recencyBoost(3, 0, half); got != 1.0 {
		t.Errorf("weight 0 should disable: got %v, want 1.0", got)
	}
	// Fresh hit gets the full bonus.
	if got := recencyBoost(0, w, half); got != 1.5 {
		t.Errorf("age 0: got %v, want 1.5", got)
	}
	// At one half-life the bonus halves: 1 + 0.5*0.5 = 1.25.
	if got := recencyBoost(half, w, half); got < 1.24 || got > 1.26 {
		t.Errorf("age=half-life: got %v, want ~1.25", got)
	}
	// Monotonic decreasing with age, asymptotic to 1.0.
	if recencyBoost(30, w, half) >= recencyBoost(7, w, half) {
		t.Error("boost should decrease with age")
	}
	if got := recencyBoost(3650, w, half); got > 1.001 {
		t.Errorf("ancient hit should approach 1.0: got %v", got)
	}
	// Negative age (clock skew) is clamped to the fresh bonus, never amplified.
	if got := recencyBoost(-5, w, half); got != 1.5 {
		t.Errorf("negative age should clamp to age 0: got %v, want 1.5", got)
	}
}

// TestHybrid_RecencyBoostsRecentDoc is the behavioral spec: given two equally
// relevant hits, the more recently indexed one ranks first when recency is on.
// Uses a dedicated project so it never collides with sibling tests that share
// the "p" project against the same test database.
func TestHybrid_RecencyBoostsRecentDoc(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fts, err := NewPgFTS(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	const proj = "recency-iso"
	// Two docs, identical content → identical ts_rank. RRF is rank-based, so
	// without a recency boost the tie order between them is undefined.
	if err := fts.Index(ctx, "rec-old", proj, "JWT authentication flow"); err != nil {
		t.Fatal(err)
	}
	if err := fts.Index(ctx, "rec-new", proj, "JWT authentication flow"); err != nil {
		t.Fatal(err)
	}
	// Backdate rec-old 60 days; keep rec-new fresh.
	if _, err := store.DB.ExecContext(ctx,
		`UPDATE pgfts_docs SET indexed_at = $1 WHERE doc_id = 'rec-old'`,
		time.Now().Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Recency on: the fresh doc must win. With a 0.5 weight at 7-day half-life,
	// rec-new gets ~1.5× and the 60-day-old doc ~1.001×, which dominates the
	// at-most-one-rank RRF gap regardless of the undefined relevance tie order.
	h := &Hybrid{FTS: fts, K: 60, RecencyWeight: 0.5, RecencyHalfLifeDays: 7}
	hits, err := h.Search(ctx, proj, "JWT authentication", "hybrid", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(hits), hits)
	}
	if hits[0].ID != "rec-new" {
		t.Errorf("recency on: top = %q, want rec-new; full = %+v", hits[0].ID, hits)
	}

	// Recency off: the boost is a no-op, so the fresh doc gets no advantage and
	// the older doc is free to tie or out-rank it (covered precisely by the
	// recencyBoost(weight=0) unit assertion). Just confirm both still surface.
	hOff := &Hybrid{FTS: fts, K: 60, RecencyWeight: 0}
	offHits, err := hOff.Search(ctx, proj, "JWT authentication", "hybrid", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(offHits) != 2 {
		t.Errorf("recency off: want 2 hits, got %+v", offHits)
	}
}

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
	// With only FTS wired, mode=hybrid should still produce results.
	store := openStore(t)
	fts, _ := NewPgFTS(context.Background(), store)
	_ = fts.Index(context.Background(), "obs-1", "p", "JWT authentication")
	h := &Hybrid{FTS: fts, K: 60}
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
	fts, _ := NewPgFTS(context.Background(), store)
	_ = fts.Index(context.Background(), "obs-1", "p", "JWT")
	h := &Hybrid{FTS: fts}
	hits, _ := h.Search(context.Background(), "p", "JWT", ModeBM25, 10)
	if len(hits) != 1 || hits[0].ID != "obs-1" {
		t.Errorf("fts passthrough via bm25 mode = %+v", hits)
	}
}

type staticEmbedder struct {
	query  []float32
	byText map[string][]float32
}

func (e staticEmbedder) Embed(_ context.Context, texts []string) ([][]float32, int, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		if vec, ok := e.byText[text]; ok {
			out[i] = vec
		} else {
			out[i] = e.query
		}
	}
	return out, len(e.query), nil
}

func TestHybrid_LoadsProvenanceForVectorAndGraphHits(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fts, err := NewPgFTS(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	const proj = "hybrid-provenance"
	if err := fts.Index(ctx, "obs-exact", proj, "exactmarker pipeline"); err != nil {
		t.Fatal(err)
	}
	// Indexed but intentionally not an FTS match for the query. It enters through
	// graph+vector and should still receive the same episodic provenance penalty.
	if err := fts.Index(ctx, "obs-graph", proj, "unrelated graph document"); err != nil {
		t.Fatal(err)
	}

	graph := NewGraphIndex(store)
	pipeline, err := graph.UpsertEntity(ctx, proj, "pipeline", "OBJECT", "TERM", 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.LinkObservationToEntity(ctx, "obs-graph", pipeline.ID); err != nil {
		t.Fatal(err)
	}

	vector := &VectorIndex{
		embedder: staticEmbedder{query: []float32{1, 0}},
		dim:      2,
		vectors: map[string][]float32{
			"obs-exact": {1, 0},
			"obs-graph": {0.99, 0.01},
		},
		projectOf: map[string]string{
			"obs-exact": proj,
			"obs-graph": proj,
		},
	}

	hybrid := &Hybrid{FTS: fts, Vector: vector, Graph: graph, K: 60}
	hits, err := hybrid.Search(ctx, proj, "exactmarker pipeline", ModeHybrid, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 {
		t.Fatalf("want at least two hits, got %+v", hits)
	}
	if hits[0].ID != "obs-exact" {
		t.Fatalf("top hit = %q, want obs-exact; hits = %+v", hits[0].ID, hits)
	}
}
