package search

import (
	"strings"
	"testing"

	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

// openStore is the shared package-level test helper for opening a Postgres-backed
// Store (skips when POSTGRES_TEST_HOST is unset). Consumed by the FTS, RRF, and
// tsquery-expander tests.
func openStore(t *testing.T) *state.Store {
	return testutil.OpenStore(t)
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
