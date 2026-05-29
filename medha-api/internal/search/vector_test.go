package search

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"testing"

	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

// fakeEmbedder is a deterministic in-process embedder that mirrors the Python
// LocalEmbedder's properties: same input → same vector, L2-normalised.
type fakeEmbedder struct{ dim int }

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, int, error) {
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		vecs[i] = f.embedOne(t)
	}
	return vecs, f.dim, nil
}

func (f *fakeEmbedder) embedOne(s string) []float32 {
	v := make([]float32, f.dim)
	// Hash each token (alnum runs lower-cased) and bump a slot.
	lower := []byte(s)
	for i := range lower {
		if lower[i] >= 'A' && lower[i] <= 'Z' {
			lower[i] += 'a' - 'A'
		}
	}
	tokStart := 0
	emit := func(start, end int) {
		if end-start < 2 {
			return
		}
		h := sha256.Sum256(lower[start:end])
		slot := int(binary.LittleEndian.Uint32(h[:4])) % f.dim
		if slot < 0 {
			slot += f.dim
		}
		sign := float32(1)
		if h[4]&1 == 0 {
			sign = -1
		}
		v[slot] += sign
	}
	for i := 0; i <= len(lower); i++ {
		if i == len(lower) || !isAlnum(lower[i]) {
			emit(tokStart, i)
			tokStart = i + 1
		}
	}
	// L2 normalise.
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n > 0 {
		inv := float32(1.0 / math.Sqrt(n))
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func openVectorStore(t *testing.T) *state.Store {
	return testutil.OpenStore(t)
}

func TestVectorIndex_Roundtrip(t *testing.T) {
	store := openVectorStore(t)
	// 384 dims keeps hash collisions sparse enough that the relevance signal
	// dominates (matches the local Python embedder's dim).
	v, err := NewVectorIndex(context.Background(), store, &fakeEmbedder{dim: 384})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	corpus := map[string]string{
		"a": "JWT authentication and token validation",
		"b": "database connection pool exhaustion",
		"c": "JWT refresh token rotation",
		"d": "unrelated yak shaving topic",
	}
	for id, text := range corpus {
		if err := v.Index(ctx, id, "p", text); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := v.Search(ctx, "p", "JWT token validation", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	// The top hit must be a JWT-related doc (a or c), not the irrelevant ones.
	if hits[0].ID != "a" && hits[0].ID != "c" {
		t.Errorf("top hit = %q, want a or c (JWT-relevant); hits=%+v", hits[0].ID, hits)
	}
	// And the top hit must score strictly higher than the unrelated one.
	var aScore, dScore float64
	for _, h := range hits {
		switch h.ID {
		case "a":
			aScore = h.Score
		case "d":
			dScore = h.Score
		}
	}
	if aScore <= dScore {
		t.Errorf("JWT doc didn't outscore unrelated doc: a=%v d=%v hits=%+v", aScore, dScore, hits)
	}
}

func TestVectorIndex_PersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()

	s1 := testutil.OpenStore(t)
	v1, _ := NewVectorIndex(ctx, s1, &fakeEmbedder{dim: 32})
	_ = v1.Index(ctx, "a", "p", "hello world")
	_ = v1.Index(ctx, "b", "p", "another doc")

	// Use the same store (PostgreSQL persists across connection re-opens).
	v2, _ := NewVectorIndex(ctx, s1, &fakeEmbedder{dim: 32})
	n, dim := v2.Stats()
	if n != 2 || dim != 32 {
		t.Errorf("after reload: docs=%d dim=%d, want 2/32", n, dim)
	}
}

func TestVectorIndex_DimensionMismatchRejected(t *testing.T) {
	store := openVectorStore(t)
	v, _ := NewVectorIndex(context.Background(), store, &fakeEmbedder{dim: 64})
	_ = v.Index(context.Background(), "a", "p", "hello")
	v.embedder = &fakeEmbedder{dim: 128} // swap to a different dim
	err := v.Index(context.Background(), "b", "p", "world")
	if err == nil {
		t.Error("expected dimension mismatch error")
	}
}

func TestCosine_Basics(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	c := []float32{1, 0}
	if got := cosine(a, b); got != 0 {
		t.Errorf("orthogonal cosine = %v, want 0", got)
	}
	if got := cosine(a, c); math.Abs(got-1) > 1e-6 {
		t.Errorf("identical cosine = %v, want 1", got)
	}
}
