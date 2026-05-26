# Task 16: Graph index & storage (Go)

- **Milestone**: M2 — Compression & Search
- **Priority**: P0
- **Depends on**: Task 6
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-16, FR-20, NFR-24; agent_mem.md §"Search Engine" (graph), §"Phase 2 Stage 3"

## Objective
Implement the SQLite-backed knowledge graph (entities + typed edges) and BFS traversal used by hybrid search — the always-available graph path when Neo4j is absent.

## Scope & Steps
- [ ] `internal/search/graph.go`: adjacency storage over `graph_entities` / `graph_edges` (Task 6 tables).
- [ ] Entity upsert (name, type, concepts, files, confidence) and edge upsert (source→target, type, confidence, sourceObsId).
- [ ] Fuzzy entity match for query terms → seed nodes.
- [ ] BFS traversal with configurable depth (default 2) + confidence filter (default ≥ 0.7).
- [ ] Return observations/memories referencing traversed entities as top ~20 candidates.
- [ ] Keep this backend authoritative for lightweight mode; Neo4j (Task 27) mirrors/enriches asynchronously.

## Files
- `go/internal/search/{graph.go,graph_test.go}`

## Acceptance Criteria
- [ ] Entities/edges upsert idempotently; duplicates merge by name+type.
- [ ] BFS respects depth and confidence thresholds.
- [ ] Query-entity fuzzy match seeds traversal correctly.
- [ ] Works with Neo4j disabled (NFR-24).

## Notes
This is the SQLite graph; Task 27 adds the richer Neo4j graph for enrichment and Cypher analytics. Keep edge types aligned with FEATURE_ANALYSIS.md §5 so both backends share vocabulary.
