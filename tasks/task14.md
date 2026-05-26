# Task 14: BM25 index & search (Go)

- **Milestone**: M2 — Compression & Search
- **Priority**: P0
- **Depends on**: Task 6
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-16, FR-17, NFR-1; agent_mem.md §"Search Engine" (BM25), §"Phase 2 Stage 1"

## Objective
Implement disk-backed BM25 keyword search over compressed observations and memories, with tokenization, stemming, and synonym expansion.

## Scope & Steps
- [ ] Decide engine: `blevesearch/bleve` vs. a hand-rolled inverted index; document the trade-off (bleve gives BM25 + persistence out of the box).
- [ ] `internal/search/bm25.go`: index `title + narrative + concepts + facts`; store mapping docId → {type, sessionId}.
- [ ] Tokenizer: lowercase, stemming, stopword removal; optional CJK handling.
- [ ] Synonym expansion list (configurable) applied at query time.
- [ ] API: `Index(doc)`, `Delete(docId)`, `Search(query, limit) []ScoredHit`.
- [ ] Persist index under the SQLite data dir; reload on startup.
- [ ] Own the BM25 index/state migration here (a new migration added to the Task 6 framework), shaped by the engine choice above — not pre-defined in Task 6.
- [ ] Hook indexing into the compressed-observation callback (Task 13) and memory creation (Task 23).

## Files
- `go/internal/search/{bm25.go,tokenizer.go,bm25_test.go}`

## Acceptance Criteria
- [ ] Indexed docs are retrievable by keyword with TF-IDF/BM25 ranking.
- [ ] Stemming matches `authenticate`/`authentication`; stopwords ignored.
- [ ] Index persists across restart.
- [ ] BM25-only search p95 < 80 ms at 10K docs (contributes to NFR-1).

## Notes
Return top ~30 candidates for fusion (Task 17), not a final top-10 — RRF needs ranked candidate lists per modality.
