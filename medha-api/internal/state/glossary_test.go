package state

import (
	"context"
	"testing"
)

func TestGlossary_MergeAndGet(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	added, err := s.MergeGlossary(ctx, "proj-a", map[string]string{
		"MFA": "Multi-Factor Authentication",
		"API": "Application Programming Interface",
		"x":   "too short abbrev, skipped",  // invalid: abbrev < 2
		"BAD": "",                            // invalid: empty expansion
	})
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	g, err := s.GetGlossary(ctx, "proj-a")
	if err != nil {
		t.Fatal(err)
	}
	if g["MFA"] != "Multi-Factor Authentication" || g["API"] != "Application Programming Interface" {
		t.Fatalf("glossary = %+v", g)
	}
	if len(g) != 2 {
		t.Fatalf("glossary size = %d, want 2", len(g))
	}
}

func TestGlossary_FirstWriteWins(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.MergeGlossary(ctx, "proj-b", map[string]string{"SSO": "Single Sign-On"}); err != nil {
		t.Fatal(err)
	}
	// A later, different expansion for the same abbrev must not overwrite.
	added, err := s.MergeGlossary(ctx, "proj-b", map[string]string{"SSO": "Some Spurious Override"})
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("added = %d, want 0 (first-write-wins)", added)
	}
	g, _ := s.GetGlossary(ctx, "proj-b")
	if g["SSO"] != "Single Sign-On" {
		t.Fatalf("SSO = %q, want original", g["SSO"])
	}
}

func TestGlossary_ProjectIsolation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.MergeGlossary(ctx, "proj-c", map[string]string{"RPS": "Requests Per Second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeGlossary(ctx, "proj-cx", map[string]string{"TPS": "Transactions Per Second"}); err != nil {
		t.Fatal(err)
	}
	g, _ := s.GetGlossary(ctx, "proj-c")
	if _, leaked := g["TPS"]; leaked {
		t.Fatalf("proj-c leaked proj-cx entry: %+v", g)
	}
	if g["RPS"] != "Requests Per Second" {
		t.Fatalf("proj-c missing own entry: %+v", g)
	}
}
