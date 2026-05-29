package search

import (
	"context"
	"testing"

	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

func openGraphStore(t *testing.T) *state.Store {
	return testutil.OpenStore(t)
}

func TestGraph_UpsertAndMatch(t *testing.T) {
	store := openGraphStore(t)
	g := NewGraphIndex(store)
	ctx := context.Background()

	a, err := g.UpsertEntity(ctx, "p", "Jose", "OBJECT", "LIBRARY", 0.9)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" {
		t.Fatal("entity id missing")
	}
	// Upsert with higher confidence — MAX should win.
	b, _ := g.UpsertEntity(ctx, "p", "Jose", "OBJECT", "LIBRARY", 0.95)
	if b.ID != a.ID {
		t.Errorf("upsert created new row: %q vs %q", b.ID, a.ID)
	}
	if b.Confidence < 0.94 {
		t.Errorf("confidence didn't update: %v", b.Confidence)
	}

	matches, err := g.MatchEntities(ctx, "p", "jose")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Name != "Jose" {
		t.Errorf("MatchEntities = %+v", matches)
	}
}

func TestGraph_BFSTraversalDepth(t *testing.T) {
	store := openGraphStore(t)
	g := NewGraphIndex(store)
	ctx := context.Background()

	a, _ := g.UpsertEntity(ctx, "p", "auth.ts", "OBJECT", "FILE", 0.95)
	b, _ := g.UpsertEntity(ctx, "p", "validateToken", "OBJECT", "FUNCTION", 0.9)
	c, _ := g.UpsertEntity(ctx, "p", "Jose", "OBJECT", "LIBRARY", 0.9)
	d, _ := g.UpsertEntity(ctx, "p", "PEM", "OBJECT", "FORMAT", 0.6)

	// a → b (validateToken in auth.ts), b → c (validateToken depends on jose),
	// c → d (jose understands PEM)
	for _, edge := range []Edge{
		{SourceID: a.ID, TargetID: b.ID, Type: "CONTAINS", Confidence: 0.9},
		{SourceID: b.ID, TargetID: c.ID, Type: "DEPENDS_ON", Confidence: 0.9},
		{SourceID: c.ID, TargetID: d.ID, Type: "USES", Confidence: 0.5},
	} {
		if err := g.AddEdge(ctx, "p", edge); err != nil {
			t.Fatal(err)
		}
	}

	visited, err := g.BFSTraverse(ctx, "p", []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Depth 2: should reach b (1) and c (2), but not d (3).
	if visited[a.ID] != 0 || visited[b.ID] != 1 {
		t.Errorf("visited = %+v", visited)
	}
	if _, reached := visited[c.ID]; !reached {
		t.Errorf("did not reach c within depth %d", g.MaxDepth)
	}
	if _, reached := visited[d.ID]; reached {
		t.Errorf("reached d at depth %d which is beyond MaxDepth %d", visited[d.ID], g.MaxDepth)
	}
}

func TestGraph_ConfidenceFilter(t *testing.T) {
	store := openGraphStore(t)
	g := NewGraphIndex(store)
	g.MinConfidence = 0.6
	ctx := context.Background()

	a, _ := g.UpsertEntity(ctx, "p", "A", "OBJECT", "", 0.9)
	b, _ := g.UpsertEntity(ctx, "p", "B", "OBJECT", "", 0.9)
	c, _ := g.UpsertEntity(ctx, "p", "C", "OBJECT", "", 0.9)

	// a → b is too weak.
	_ = g.AddEdge(ctx, "p", Edge{SourceID: a.ID, TargetID: b.ID, Type: "WEAK", Confidence: 0.3})
	// a → c is strong.
	_ = g.AddEdge(ctx, "p", Edge{SourceID: a.ID, TargetID: c.ID, Type: "STRONG", Confidence: 0.9})

	visited, _ := g.BFSTraverse(ctx, "p", []string{a.ID})
	if _, ok := visited[b.ID]; ok {
		t.Error("low-confidence edge traversed despite MinConfidence")
	}
	if _, ok := visited[c.ID]; !ok {
		t.Error("strong edge not traversed")
	}
}

func TestGraph_SearchReturnsLinkedObservations(t *testing.T) {
	store := openGraphStore(t)
	g := NewGraphIndex(store)
	ctx := context.Background()

	jose, _ := g.UpsertEntity(ctx, "p", "Jose", "OBJECT", "LIBRARY", 0.9)
	auth, _ := g.UpsertEntity(ctx, "p", "auth.ts", "OBJECT", "FILE", 0.9)
	_ = g.AddEdge(ctx, "p", Edge{SourceID: auth.ID, TargetID: jose.ID, Type: "DEPENDS_ON", Confidence: 0.9})

	// Link two observations to entities.
	_ = g.LinkObservationToEntity(ctx, "obs-1", auth.ID)
	_ = g.LinkObservationToEntity(ctx, "obs-2", jose.ID)

	hits, err := g.Search(ctx, "p", "jose", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 {
		t.Errorf("expected hits for obs-1 (BFS) and obs-2 (seed), got %+v", hits)
	}
	// obs-2 is directly linked to the seed entity, so should outrank obs-1.
	if hits[0].ID != "obs-2" {
		t.Errorf("top hit = %q, want obs-2", hits[0].ID)
	}
}

func TestExtractObservationID(t *testing.T) {
	cases := map[string]string{
		`{"observation_id":"obs-abc"}`:           "obs-abc",
		`{"other":"x","observation_id":"obs-y"}`: "obs-y",
		`{"no_match":"here"}`:                    "",
	}
	for in, want := range cases {
		if got := extractObservationID(in); got != want {
			t.Errorf("extractObservationID(%q) = %q, want %q", in, got, want)
		}
	}
}
