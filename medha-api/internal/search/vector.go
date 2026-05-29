package search

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/udai-kiran/medha/internal/state"
)

// Embedder is the local Go-side surface for fetching embeddings from the
// Python sidecar (POST /embed). The struct in vector_index.go talks to this
// rather than to net/http directly so tests can inject a fake.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, int, error)
}

// PythonEmbedder calls the Python service.
type PythonEmbedder struct {
	BaseURL string
	HTTP    *http.Client
}

// Embed batches the texts to POST /embed and returns float32 vectors.
func (p *PythonEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, int, error) {
	if p == nil || p.BaseURL == "" {
		return nil, 0, errors.New("PythonEmbedder: BaseURL required")
	}
	body, err := json.Marshal(map[string]any{"texts": texts})
	if err != nil {
		return nil, 0, err
	}
	url := strings.TrimRight(p.BaseURL, "/") + "/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("embed: status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Embeddings [][]float64 `json:"embeddings"`
		Dimension  int         `json:"dimension"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	vectors := make([][]float32, len(out.Embeddings))
	for i, v := range out.Embeddings {
		vv := make([]float32, len(v))
		for j, x := range v {
			vv[j] = float32(x)
		}
		vectors[i] = vv
	}
	return vectors, out.Dimension, nil
}

// VectorIndex stores float32 embeddings keyed by doc id and answers cosine-
// similarity queries. Persistence is via a small table; the hot path
// loads vectors into memory on first use so similarity is a tight loop.
//
// Dimension is locked at first Index call to prevent mixed-dim corpora; a
// project that wants to switch providers must reset the index.
type VectorIndex struct {
	store    *state.Store
	embedder Embedder

	mu        sync.RWMutex
	dim       int
	vectors   map[string][]float32 // docID → vector
	projectOf map[string]string
}

// NewVectorIndex applies the schema migration and warms in-memory state.
func NewVectorIndex(ctx context.Context, s *state.Store, e Embedder) (*VectorIndex, error) {
	if err := ensureVectorSchema(ctx, s.DB); err != nil {
		return nil, err
	}
	v := &VectorIndex{
		store:     s,
		embedder:  e,
		vectors:   make(map[string][]float32),
		projectOf: make(map[string]string),
	}
	if err := v.warm(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

func ensureVectorSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS vector_docs (
            doc_id     TEXT PRIMARY KEY,
            project    TEXT NOT NULL DEFAULT '',
            dim        INTEGER NOT NULL,
            vector     BYTEA NOT NULL,
            indexed_at TEXT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_vector_docs_project ON vector_docs(project)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// warm loads every persisted vector into memory.
func (v *VectorIndex) warm(ctx context.Context) error {
	rows, err := v.store.DB.QueryContext(ctx, `SELECT doc_id, project, dim, vector FROM vector_docs`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	v.mu.Lock()
	defer v.mu.Unlock()
	for rows.Next() {
		var docID, project string
		var dim int
		var blob []byte
		if err := rows.Scan(&docID, &project, &dim, &blob); err != nil {
			return err
		}
		if v.dim == 0 {
			v.dim = dim
		}
		vec, err := decodeVector(blob, dim)
		if err != nil {
			return err
		}
		v.vectors[docID] = vec
		v.projectOf[docID] = project
	}
	return rows.Err()
}

// Index embeds ``text`` via the configured Embedder and stores the vector.
// Reindexing a doc replaces its prior vector.
func (v *VectorIndex) Index(ctx context.Context, docID, project, text string) error {
	if docID == "" {
		return errors.New("VectorIndex.Index: docID required")
	}
	if v.embedder == nil {
		return errors.New("VectorIndex.Index: no embedder configured")
	}
	vecs, dim, err := v.embedder.Embed(ctx, []string{text})
	if err != nil {
		return err
	}
	if len(vecs) != 1 || len(vecs[0]) != dim {
		return fmt.Errorf("VectorIndex.Index: malformed embedding response")
	}
	vec := vecs[0]

	v.mu.Lock()
	if v.dim != 0 && v.dim != dim {
		v.mu.Unlock()
		return fmt.Errorf("VectorIndex.Index: dimension mismatch (have %d, got %d)", v.dim, dim)
	}
	v.dim = dim
	v.vectors[docID] = vec
	v.projectOf[docID] = project
	v.mu.Unlock()

	blob, err := encodeVector(vec)
	if err != nil {
		return err
	}
	_, err = v.store.DB.ExecContext(ctx, `
        INSERT INTO vector_docs (doc_id, project, dim, vector, indexed_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT(doc_id) DO UPDATE SET
            project    = excluded.project,
            dim        = excluded.dim,
            vector     = excluded.vector,
            indexed_at = excluded.indexed_at
    `, docID, project, dim, blob, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// Delete removes a doc from both memory and storage.
func (v *VectorIndex) Delete(ctx context.Context, docID string) error {
	v.mu.Lock()
	delete(v.vectors, docID)
	delete(v.projectOf, docID)
	v.mu.Unlock()
	_, err := v.store.DB.ExecContext(ctx, `DELETE FROM vector_docs WHERE doc_id = $1`, docID)
	return err
}

// Search embeds the query and returns top-k cosine similarities, filtered by project.
func (v *VectorIndex) Search(ctx context.Context, project, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	if v.embedder == nil {
		return nil, errors.New("VectorIndex.Search: no embedder configured")
	}

	v.mu.RLock()
	have := len(v.vectors) > 0
	v.mu.RUnlock()
	if !have {
		return nil, nil
	}

	qvecs, _, err := v.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(qvecs) == 0 {
		return nil, nil
	}
	q := qvecs[0]

	v.mu.RLock()
	defer v.mu.RUnlock()

	type scored struct {
		id    string
		score float64
	}
	scored_list := make([]scored, 0, len(v.vectors))
	for id, vec := range v.vectors {
		if project != "" && v.projectOf[id] != project {
			continue
		}
		s := cosine(q, vec)
		if math.IsNaN(s) {
			continue
		}
		scored_list = append(scored_list, scored{id, s})
	}
	sort.SliceStable(scored_list, func(i, j int) bool { return scored_list[i].score > scored_list[j].score })

	if len(scored_list) > limit {
		scored_list = scored_list[:limit]
	}
	hits := make([]Hit, len(scored_list))
	for i, s := range scored_list {
		hits[i] = Hit{ID: s.id, Score: s.score}
	}
	return hits, nil
}

// cosine returns the cosine similarity of two equally-dimensioned vectors.
// Both inputs are expected to be L2-normalised; we still compute the full
// formula in case a provider doesn't normalise.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return math.NaN()
	}
	var dot, na, nb float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// encodeVector writes the vector as little-endian float32 blob — compact and
// fast to round-trip.
func encodeVector(v []float32) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, len(v)*4))
	for _, x := range v {
		if err := binary.Write(buf, binary.LittleEndian, x); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeVector(b []byte, dim int) ([]float32, error) {
	if len(b) != dim*4 {
		return nil, fmt.Errorf("decodeVector: blob len %d, want %d", len(b), dim*4)
	}
	out := make([]float32, dim)
	r := bytes.NewReader(b)
	for i := 0; i < dim; i++ {
		if err := binary.Read(r, binary.LittleEndian, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Compile-time assertion that VectorIndex satisfies SearchEngine.
var _ SearchEngine = (*VectorIndex)(nil)

// Stats exposes index statistics.
func (v *VectorIndex) Stats() (totalDocs, dimension int) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.vectors), v.dim
}
