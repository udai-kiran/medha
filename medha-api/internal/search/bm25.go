package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/udai-kiran/medha/internal/state"
)

// Hit is a single ranked result. Score range and meaning depend on the engine
// — RRF normalises across engines.
type Hit struct {
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// SearchEngine is the contract every single-modality engine implements
// (BM25, vector, graph). The hybrid orchestrator (Task 17) consumes this.
type SearchEngine interface {
	// Search returns up to ``limit`` ranked hits for the given project + query.
	Search(ctx context.Context, project, query string, limit int) ([]Hit, error)
}

// BM25 is the keyword search engine. It owns its own tables (created via
// Migrate) so Task 6's core schema doesn't pre-commit to index-specific shape.
//
// BM25 parameters use the standard k1=1.2, b=0.75 defaults.
type BM25 struct {
	store *state.Store
	mu    sync.RWMutex
	// docLen caches |D| per docID to avoid recomputing on each query.
	docLen map[string]int
	// avgDocLen is the running average across all indexed docs.
	avgDocLen float64
	// dfCache caches per-term document frequency to avoid repeated COUNTs.
	dfCache map[string]int
	// totalDocs is the count of indexed documents.
	totalDocs int
}

// NewBM25 wires a BM25 engine to a Store and applies its schema migration.
func NewBM25(ctx context.Context, s *state.Store) (*BM25, error) {
	if err := ensureBM25Schema(ctx, s.DB); err != nil {
		return nil, fmt.Errorf("NewBM25: %w", err)
	}
	b := &BM25{
		store:   s,
		docLen:  make(map[string]int),
		dfCache: make(map[string]int),
	}
	if err := b.warm(ctx); err != nil {
		return nil, fmt.Errorf("NewBM25: warm: %w", err)
	}
	return b, nil
}

// ensureBM25Schema creates the BM25 index tables.
func ensureBM25Schema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bm25_docs (
            doc_id     TEXT PRIMARY KEY,
            project    TEXT NOT NULL DEFAULT '',
            doc_len    INTEGER NOT NULL,
            indexed_at TEXT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_bm25_docs_project ON bm25_docs(project)`,
		`CREATE TABLE IF NOT EXISTS bm25_postings (
            term      TEXT NOT NULL,
            doc_id    TEXT NOT NULL,
            tf        INTEGER NOT NULL,
            PRIMARY KEY (term, doc_id)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_bm25_postings_term ON bm25_postings(term)`,
		`CREATE INDEX IF NOT EXISTS idx_bm25_postings_docid ON bm25_postings(doc_id)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// warm reads aggregate counts so the first query doesn't pay a cold scan.
func (b *BM25) warm(ctx context.Context) error {
	var n int
	var total int64
	row := b.store.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(doc_len), 0) FROM bm25_docs`)
	if err := row.Scan(&n, &total); err != nil {
		return err
	}
	b.totalDocs = n
	if n > 0 {
		b.avgDocLen = float64(total) / float64(n)
	}
	return nil
}

// Index adds (or replaces) a document. ``text`` is tokenized, term frequencies
// counted, and persisted to bm25_postings.
func (b *BM25) Index(ctx context.Context, docID, project, text string) error {
	if docID == "" {
		return errors.New("BM25.Index: docID required")
	}
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return b.upsertDoc(ctx, docID, project, 0, nil)
	}

	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}

	return b.upsertDoc(ctx, docID, project, len(tokens), tf)
}

func (b *BM25) upsertDoc(ctx context.Context, docID, project string, docLen int, tf map[string]int) error {
	tx, err := b.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM bm25_postings WHERE doc_id = $1`, docID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO bm25_docs (doc_id, project, doc_len, indexed_at)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT(doc_id) DO UPDATE SET
            project    = excluded.project,
            doc_len    = excluded.doc_len,
            indexed_at = excluded.indexed_at
    `, docID, project, docLen, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for term, count := range tf {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO bm25_postings (term, doc_id, tf)
            VALUES ($1, $2, $3)
            ON CONFLICT(term, doc_id) DO UPDATE SET tf = excluded.tf
        `, term, docID, count); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	b.mu.Lock()
	b.dfCache = make(map[string]int)
	b.mu.Unlock()
	return b.warm(ctx)
}

// Delete drops a doc from the index.
func (b *BM25) Delete(ctx context.Context, docID string) error {
	if _, err := b.store.DB.ExecContext(ctx, `DELETE FROM bm25_postings WHERE doc_id = $1`, docID); err != nil {
		return err
	}
	if _, err := b.store.DB.ExecContext(ctx, `DELETE FROM bm25_docs WHERE doc_id = $1`, docID); err != nil {
		return err
	}
	b.mu.Lock()
	b.dfCache = make(map[string]int)
	b.mu.Unlock()
	return b.warm(ctx)
}

// Search returns ranked Hits for the query. Standard BM25 with k1=1.2, b=0.75.
func (b *BM25) Search(ctx context.Context, project, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	terms := ExpandSynonyms(Tokenize(query))
	if len(terms) == 0 {
		return nil, nil
	}
	if b.totalDocs == 0 {
		return nil, nil
	}

	const (
		k1 = 1.2
		bp = 0.75
	)
	idfByTerm := make(map[string]float64, len(terms))
	for _, t := range terms {
		df, err := b.docFrequency(ctx, t)
		if err != nil {
			return nil, err
		}
		if df == 0 {
			continue
		}
		idfByTerm[t] = math.Log((float64(b.totalDocs)-float64(df)+0.5)/(float64(df)+0.5) + 1)
	}
	if len(idfByTerm) == 0 {
		return nil, nil
	}

	scores := make(map[string]float64)
	for term, idf := range idfByTerm {
		rows, err := b.store.DB.QueryContext(ctx, `
            SELECT p.doc_id, p.tf, d.doc_len
            FROM bm25_postings p JOIN bm25_docs d ON d.doc_id = p.doc_id
            WHERE p.term = $1 AND ($2 = '' OR d.project = $2)
        `, term, project)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var docID string
			var tf, docLen int
			if err := rows.Scan(&docID, &tf, &docLen); err != nil {
				_ = rows.Close()
				return nil, err
			}
			numer := float64(tf) * (k1 + 1)
			denom := float64(tf) + k1*(1-bp+bp*float64(docLen)/avg(b.avgDocLen))
			scores[docID] += idf * (numer / denom)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	hits := make([]Hit, 0, len(scores))
	for id, sc := range scores {
		hits = append(hits, Hit{ID: id, Score: sc})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (b *BM25) docFrequency(ctx context.Context, term string) (int, error) {
	b.mu.RLock()
	if df, ok := b.dfCache[term]; ok {
		b.mu.RUnlock()
		return df, nil
	}
	b.mu.RUnlock()

	var df int
	if err := b.store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bm25_postings WHERE term = $1`, term,
	).Scan(&df); err != nil {
		return 0, err
	}
	b.mu.Lock()
	b.dfCache[term] = df
	b.mu.Unlock()
	return df, nil
}

// Stats exposes index statistics for tests/observability.
func (b *BM25) Stats() (totalDocs int, avgDocLen float64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalDocs, b.avgDocLen
}

// avg ensures we never divide by zero in the BM25 length norm.
func avg(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

// HighlightTerms returns the indexed terms present in s — used by upper layers
// to build snippets without re-tokenizing.
func HighlightTerms(s string) []string {
	t := Tokenize(s)
	out := make([]string, 0, len(t))
	seen := make(map[string]struct{}, len(t))
	for _, w := range t {
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

// String exposes a debug snapshot of the engine config.
func (b *BM25) String() string {
	return fmt.Sprintf("BM25{totalDocs=%d avgDocLen=%.2f}", b.totalDocs, b.avgDocLen)
}

// Compile-time assertion that BM25 satisfies SearchEngine.
var _ SearchEngine = (*BM25)(nil)

// HelperBuildText is used by Task 18 to assemble the indexable text for a
// CompressedObservation. Centralised here so the indexer and the search side
// share the same input shape.
func HelperBuildText(title, subtitle, narrative string, concepts, files, facts []string) string {
	parts := []string{title, subtitle, narrative}
	parts = append(parts, concepts...)
	parts = append(parts, files...)
	parts = append(parts, facts...)
	return strings.Join(parts, " ")
}
