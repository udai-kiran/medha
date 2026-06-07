package search

import (
	"context"
	"testing"
)

func TestPgFTS_IndexAndSearch(t *testing.T) {
	s := openStore(t)
	fts, err := NewPgFTS(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	docs := map[string]string{
		"obs-1": "Read authentication middleware implementing JWT validation in src/auth.ts",
		"obs-2": "Run database migration to add user table",
		"obs-3": "Implement JWT token refresh endpoint with refresh token validation",
		"obs-4": "Configure CORS for the API gateway",
		"obs-5": "Investigate database connection pool exhaustion",
	}
	for id, txt := range docs {
		if err := fts.Index(ctx, id, "proj", txt); err != nil {
			t.Fatalf("Index %s: %v", id, err)
		}
	}

	// websearch_to_tsquery uses implicit AND: "JWT authentication" requires
	// BOTH terms. Only obs-1 and obs-3 contain "JWT"; only obs-1 also has
	// "authentication", so at least obs-1 must be the top hit.
	hits, err := fts.Search(ctx, "proj", "JWT authentication", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 {
		t.Fatalf("expected ≥1 hit, got %d", len(hits))
	}
	if hits[0].ID != "obs-1" {
		t.Errorf("top hit = %q, want obs-1 (has both JWT and authentication)", hits[0].ID)
	}

	dbHits, err := fts.Search(ctx, "proj", "database pool", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range dbHits {
		if h.ID == "obs-5" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected obs-5 in database/pool results, got %v", dbHits)
	}
}

func TestPgFTS_SnippetsPopulated(t *testing.T) {
	s := openStore(t)
	fts, err := NewPgFTS(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-1", "p", "JWT authentication validates tokens in middleware")

	hits, err := fts.Search(ctx, "p", "JWT", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Snippet == "" {
		t.Error("expected non-empty snippet from ts_headline")
	}
}

func TestPgFTS_ProjectIsolation(t *testing.T) {
	s := openStore(t)
	fts, _ := NewPgFTS(context.Background(), s)
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-a", "proj-a", "authentication token")
	_ = fts.Index(ctx, "obs-b", "proj-b", "authentication token")

	hits, _ := fts.Search(ctx, "proj-a", "authentication", 10)
	if len(hits) != 1 || hits[0].ID != "obs-a" {
		t.Errorf("project-a isolation failed: %v", hits)
	}
}

func TestPgFTS_ReindexReplaces(t *testing.T) {
	s := openStore(t)
	fts, _ := NewPgFTS(context.Background(), s)
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-1", "p", "completely unrelated yak shaving topic")
	_ = fts.Index(ctx, "obs-1", "p", "authentication and JWT")

	hits, _ := fts.Search(ctx, "p", "yak", 10)
	if len(hits) != 0 {
		t.Errorf("old terms should not match after reindex: %v", hits)
	}
	hits, _ = fts.Search(ctx, "p", "authentication", 10)
	if len(hits) != 1 {
		t.Errorf("new terms should match after reindex, got %v", hits)
	}
}

func TestPgFTS_DeleteRemoves(t *testing.T) {
	s := openStore(t)
	fts, _ := NewPgFTS(context.Background(), s)
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-1", "p", "authentication and JWT")
	_ = fts.Delete(ctx, "obs-1")
	hits, _ := fts.Search(ctx, "p", "authentication", 10)
	if len(hits) != 0 {
		t.Errorf("after delete, expected no hits: %v", hits)
	}
}

func TestPgFTS_EmptyQuery(t *testing.T) {
	s := openStore(t)
	fts, _ := NewPgFTS(context.Background(), s)
	hits, err := fts.Search(context.Background(), "p", "", 10)
	if err != nil || len(hits) != 0 {
		t.Errorf("empty query: hits=%v err=%v", hits, err)
	}
}

func TestPgFTS_GetDocumentTexts(t *testing.T) {
	s := openStore(t)
	fts, _ := NewPgFTS(context.Background(), s)
	ctx := context.Background()
	_ = fts.Index(ctx, "obs-1", "p", "text one")
	_ = fts.Index(ctx, "obs-2", "p", "text two")

	texts, err := fts.GetDocumentTexts(ctx, []string{"obs-1", "obs-2", "obs-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if texts["obs-1"] != "text one" {
		t.Errorf("obs-1 text = %q", texts["obs-1"])
	}
	if texts["obs-2"] != "text two" {
		t.Errorf("obs-2 text = %q", texts["obs-2"])
	}
	if _, ok := texts["obs-missing"]; ok {
		t.Error("obs-missing should not be in result")
	}
}
