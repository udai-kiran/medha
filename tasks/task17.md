# Task 17: RRF fusion + hybrid orchestrator

- **Milestone**: M2 — Compression & Search
- **Priority**: P0
- **Depends on**: Task 14, Task 15, Task 16
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-16, FR-18, NFR-1; agent_mem.md §"Phase 2 Stage 4–6", §"Key Design Decisions #3"

## Objective
Combine BM25, vector, and graph candidate lists via Reciprocal Rank Fusion (k=60), apply diversity boosting, and return ranked results with snippets.

## Scope & Steps
- [ ] `internal/search/rrf.go`: `RRF score = Σ 1/(k + rank_i)` across modalities; k configurable (default 60).
- [ ] `internal/search/hybrid.go`: run the three searches concurrently (goroutines), gather candidates, fuse.
- [ ] Diversity boost: cap results per `sessionId` (default max 3).
- [ ] Fetch full objects for the fused top-N from the state layer; attach snippet + relevance.
- [ ] Support `mode` param: `hybrid` (default), `bm25`, `vector`, `graph` (FR-17) to run a single path.
- [ ] Normalize relevance to 0–1 for the response.
- [ ] Add tracing spans per modality (feeds Task 29).

## Files
- `go/internal/search/{rrf.go,hybrid.go,hybrid_test.go}`

## Acceptance Criteria
- [ ] Fusion ranking matches a hand-computed RRF example in a unit test.
- [ ] The three searches run concurrently; total latency ≈ max(individual), not sum.
- [ ] Diversity cap enforced; single-mode bypasses fusion.
- [ ] Hybrid search p95 < 150 ms at 10K docs (NFR-1).

## Notes
Run modalities in parallel and merge — sequential execution would blow the latency budget. Graph path may return empty for non-entity queries; fusion must handle empty lists gracefully.
