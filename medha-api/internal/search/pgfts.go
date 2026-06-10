package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/udai-kiran/medha/internal/state"
)

// PgFTS is a PostgreSQL full-text search engine backed by tsvector/tsquery with
// a GIN index. It replaces the hand-rolled BM25 inverted-index tables. Ranking
// uses ts_rank_cd (cover density), which penalises scattered term matches and
// outperforms raw BM25 on long documents. Snippets are produced by ts_headline
// so the search handler no longer needs to clip raw narrative text.
type PgFTS struct {
	store *state.Store
}

// NewPgFTS creates the schema tables/indexes (idempotent) and returns a ready engine.
func NewPgFTS(ctx context.Context, s *state.Store) (*PgFTS, error) {
	if err := ensurePgFTSSchema(ctx, s.DB); err != nil {
		return nil, fmt.Errorf("NewPgFTS: %w", err)
	}
	return &PgFTS{store: s}, nil
}

func ensurePgFTSSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		// GENERATED ALWAYS AS ... STORED keeps search_vec in sync automatically.
		// Requires Postgres 12+.
		// provenance: user|extracted|episodic|system — drives post-RRF priority boost.
		`CREATE TABLE IF NOT EXISTS pgfts_docs (
            doc_id     TEXT PRIMARY KEY,
            project    TEXT NOT NULL DEFAULT '',
            content    TEXT NOT NULL DEFAULT '',
            provenance TEXT NOT NULL DEFAULT 'episodic',
            search_vec tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
            indexed_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_pgfts_gin     ON pgfts_docs USING GIN(search_vec)`,
		`CREATE INDEX IF NOT EXISTS idx_pgfts_project ON pgfts_docs(project)`,
		// Idempotent column add for DBs that already have the table without provenance.
		// Must run before the index on provenance.
		`ALTER TABLE pgfts_docs ADD COLUMN IF NOT EXISTS provenance TEXT NOT NULL DEFAULT 'episodic'`,
		`CREATE INDEX IF NOT EXISTS idx_pgfts_provenance ON pgfts_docs(provenance)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// Index upserts a document with the default provenance ("episodic").
// Use IndexWithProvenance when the source is a user memory or pipeline memory.
func (p *PgFTS) Index(ctx context.Context, docID, project, content string) error {
	return p.IndexWithProvenance(ctx, docID, project, content, "episodic")
}

// IndexWithProvenance upserts a document and records where it came from.
// provenance should be one of: user, extracted, episodic, system.
func (p *PgFTS) IndexWithProvenance(ctx context.Context, docID, project, content, provenance string) error {
	if provenance == "" {
		provenance = "episodic"
	}
	_, err := p.store.DB.ExecContext(ctx, `
        INSERT INTO pgfts_docs (doc_id, project, content, provenance, indexed_at)
        VALUES ($1, $2, $3, $4, now())
        ON CONFLICT(doc_id) DO UPDATE SET
            project    = excluded.project,
            content    = excluded.content,
            provenance = excluded.provenance,
            indexed_at = now()
    `, docID, project, content, provenance)
	return err
}

// IndexedAt returns the indexed_at timestamp for each requested doc_id, keyed by
// doc_id. IDs not present in the index are omitted from the map. Used by the
// hybrid orchestrator to apply a recency boost across all engine legs (the
// timestamp lives in the FTS table regardless of whether a hit came from FTS,
// vector, or graph).
func (p *PgFTS) IndexedAt(ctx context.Context, ids []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.store.DB.QueryContext(ctx,
		`SELECT doc_id, indexed_at FROM pgfts_docs WHERE doc_id = ANY($1)`,
		pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id string
			ts time.Time
		)
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, err
		}
		out[id] = ts
	}
	return out, rows.Err()
}

// ProvenanceFor returns provenance labels for indexed documents. IDs absent from
// the FTS table are omitted. Hybrid search uses this to apply provenance boosts
// consistently to hits that entered through vector or graph legs.
func (p *PgFTS) ProvenanceFor(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := p.store.DB.QueryContext(ctx,
		`SELECT doc_id, provenance FROM pgfts_docs WHERE doc_id = ANY($1)`,
		pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, provenance string
		if err := rows.Scan(&id, &provenance); err != nil {
			return nil, err
		}
		out[id] = provenance
	}
	return out, rows.Err()
}

// Delete removes a document from the index.
func (p *PgFTS) Delete(ctx context.Context, docID string) error {
	_, err := p.store.DB.ExecContext(ctx, `DELETE FROM pgfts_docs WHERE doc_id = $1`, docID)
	return err
}

// Search executes a full-text query using websearch_to_tsquery (handles natural
// language, phrases, and implicit AND). Hits are ranked by ts_rank_cd and carry
// a highlighted snippet.
func (p *PgFTS) Search(ctx context.Context, project, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	if query == "" {
		return nil, nil
	}
	// CROSS JOIN LATERAL computes the tsquery once and reuses it in both
	// the WHERE predicate and the ranking/headline functions.
	rows, err := p.store.DB.QueryContext(ctx, `
        SELECT d.doc_id,
               ts_rank_cd(d.search_vec, q.q, 32) AS score,
               ts_headline('english', d.content, q.q,
                           'MaxWords=20,MinWords=5,HighlightAll=false') AS snippet,
               d.provenance
        FROM pgfts_docs d
        CROSS JOIN LATERAL (SELECT websearch_to_tsquery('english', $1)) AS q(q)
        WHERE ($2 = '' OR d.project = $2)
          AND d.search_vec @@ q.q
        ORDER BY score DESC
        LIMIT $3
    `, query, project, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Score, &h.Snippet, &h.Provenance); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// SearchWithTSQuery executes pre-compiled ts_query strings (from a TSQueryExpander)
// ORed together. Gives the LLM agent's structured expansion precedence over the
// plain websearch_to_tsquery path. Falls back gracefully: if to_tsquery rejects
// a string (invalid syntax), the error bubbles up so the caller can retry with
// Search(). All tsquery strings must use to_tsquery syntax (&, |, <->, parens).
func (p *PgFTS) SearchWithTSQuery(ctx context.Context, project string, tsqueries []string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	combined := buildCombinedTSQuery(tsqueries)
	if combined == "" {
		return nil, nil
	}
	rows, err := p.store.DB.QueryContext(ctx, `
        SELECT d.doc_id,
               ts_rank_cd(d.search_vec, q.q, 32) AS score,
               ts_headline('english', d.content, q.q,
                           'MaxWords=20,MinWords=5,HighlightAll=false') AS snippet,
               d.provenance
        FROM pgfts_docs d
        CROSS JOIN LATERAL (SELECT to_tsquery('english', $1)) AS q(q)
        WHERE ($2 = '' OR d.project = $2)
          AND d.search_vec @@ q.q
        ORDER BY score DESC
        LIMIT $3
    `, combined, project, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Score, &h.Snippet, &h.Provenance); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// buildCombinedTSQuery joins ts_query strings with OR, wrapping each in parens
// so operator precedence is explicit.
func buildCombinedTSQuery(tsqueries []string) string {
	parts := make([]string, 0, len(tsqueries))
	for _, q := range tsqueries {
		q = strings.TrimSpace(q)
		if q != "" {
			parts = append(parts, "("+q+")")
		}
	}
	return strings.Join(parts, " | ")
}

// GetDocumentTexts fetches the raw content for a set of doc IDs in one round-trip.
// IDs absent from the index are silently omitted. Used by the hybrid reranker to
// assemble (query, document) pairs without per-hit queries.
func (p *PgFTS) GetDocumentTexts(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := p.store.DB.QueryContext(ctx,
		`SELECT doc_id, content FROM pgfts_docs WHERE doc_id = ANY($1)`,
		pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string, len(ids))
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return nil, err
		}
		out[id] = content
	}
	return out, rows.Err()
}

// Stats returns the number of indexed documents. Used by the diagnose endpoint.
func (p *PgFTS) Stats(ctx context.Context) (int, error) {
	var n int
	err := p.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pgfts_docs`).Scan(&n)
	return n, err
}

// Compile-time assertion that PgFTS satisfies Engine.
var _ Engine = (*PgFTS)(nil)
