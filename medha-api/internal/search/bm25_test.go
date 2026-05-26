package search

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/udai-kiran/medha/internal/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bm25-test.db")
	s, err := state.Open(context.Background(), state.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTokenize_LowercasesAndStems(t *testing.T) {
	tokens := Tokenize("Authentication, authentications and AUTH are authenticated.")
	joined := strings.Join(tokens, " ")
	// Stems vary; we just assert lowercase + no punctuation + stopwords gone.
	if strings.ContainsAny(joined, "ABCDEFGHIJKLMNOPQRSTUVWXYZ,.") {
		t.Errorf("got %q", joined)
	}
	if strings.Contains(joined, "the ") || strings.Contains(joined, " and ") {
		t.Errorf("stopwords leaked: %q", joined)
	}
}

func TestTokenize_DropsShort(t *testing.T) {
	tokens := Tokenize("a b cd ef gh")
	for _, tok := range tokens {
		if len(tok) < 2 {
			t.Errorf("token %q below min length", tok)
		}
	}
}

func TestTokenize_CJK(t *testing.T) {
	tokens := Tokenize("認証 token 認証")
	// Each CJK char should become its own token.
	cjkCount := 0
	for _, tok := range tokens {
		for _, r := range tok {
			if r >= 0x4E00 && r <= 0x9FFF {
				cjkCount++
			}
		}
	}
	if cjkCount < 2 {
		t.Errorf("CJK tokens missing: %v", tokens)
	}
}

func TestExpandSynonyms(t *testing.T) {
	out := ExpandSynonyms([]string{"auth"})
	if !contains(out, "auth") || !contains(out, "authentication") {
		t.Errorf("missing synonyms: %v", out)
	}
}

func TestBM25_IndexAndSearch(t *testing.T) {
	s := openStore(t)
	b, err := NewBM25(context.Background(), s)
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
		if err := b.Index(ctx, id, "proj", txt); err != nil {
			t.Fatalf("Index %s: %v", id, err)
		}
	}

	hits, err := b.Search(ctx, "proj", "JWT authentication", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected ≥2 hits, got %d", len(hits))
	}
	// Top hit must mention JWT.
	top := hits[0].ID
	if top != "obs-1" && top != "obs-3" {
		t.Errorf("top hit = %q, want obs-1 or obs-3", top)
	}

	// Database query should reach obs-2 and obs-5.
	dbHits, _ := b.Search(ctx, "proj", "database pool", 10)
	ids := map[string]bool{}
	for _, h := range dbHits {
		ids[h.ID] = true
	}
	if !ids["obs-5"] {
		t.Errorf("expected obs-5 in database/pool results, got %v", dbHits)
	}
}

func TestBM25_ProjectIsolation(t *testing.T) {
	s := openStore(t)
	b, _ := NewBM25(context.Background(), s)
	ctx := context.Background()
	_ = b.Index(ctx, "obs-a", "proj-a", "authentication token")
	_ = b.Index(ctx, "obs-b", "proj-b", "authentication token")

	hits, _ := b.Search(ctx, "proj-a", "authentication", 10)
	if len(hits) != 1 || hits[0].ID != "obs-a" {
		t.Errorf("project-a results = %v", hits)
	}
}

func TestBM25_ReindexReplaces(t *testing.T) {
	s := openStore(t)
	b, _ := NewBM25(context.Background(), s)
	ctx := context.Background()
	_ = b.Index(ctx, "obs-1", "p", "completely unrelated yak shaving topic")
	_ = b.Index(ctx, "obs-1", "p", "authentication and JWT")
	hits, _ := b.Search(ctx, "p", "yak", 10)
	if len(hits) != 0 {
		t.Errorf("old terms should not match after reindex: %v", hits)
	}
	hits, _ = b.Search(ctx, "p", "authentication", 10)
	if len(hits) != 1 {
		t.Errorf("new terms should match, got %v", hits)
	}
}

func TestBM25_DeleteRemoves(t *testing.T) {
	s := openStore(t)
	b, _ := NewBM25(context.Background(), s)
	ctx := context.Background()
	_ = b.Index(ctx, "obs-1", "p", "authentication and JWT")
	_ = b.Delete(ctx, "obs-1")
	hits, _ := b.Search(ctx, "p", "authentication", 10)
	if len(hits) != 0 {
		t.Errorf("after delete, expected no hits: %v", hits)
	}
}

func TestBM25_EmptyQuery(t *testing.T) {
	s := openStore(t)
	b, _ := NewBM25(context.Background(), s)
	hits, err := b.Search(context.Background(), "p", "", 10)
	if err != nil || len(hits) != 0 {
		t.Errorf("empty query: hits=%v err=%v", hits, err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
