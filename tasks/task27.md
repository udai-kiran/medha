# Task 27: Neo4j graph integration

- **Milestone**: M4 — REST & MCP
- **Priority**: P1
- **Depends on**: Task 16, Task 20
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-20, FR-25, NFR-24; agent_mem.md §"Neo4j Integration", reference/DESIGN.md schema

## Objective
Add the richer Neo4j graph backend for entity CRUD, typed relationships, and Cypher analytics — mirroring the SQLite graph and enabling enrichment, while remaining optional.

## Scope & Steps
- [ ] `internal/graph/neo4j.go`: Bolt driver connection + pooling; feature-detect availability at startup (degrade to SQLite-only per NFR-24).
- [ ] `internal/graph/entity.go`: entity CRUD with POLE+O labels, embedding, confidence, enrichment fields.
- [ ] `internal/graph/relationship.go`: typed edges with confidence + source provenance.
- [ ] `internal/graph/enrichment.go`: read enrichment fields back into search results.
- [ ] Cypher: entity creation, relationship creation, BFS traversal, enrichment lookup (from agent_mem.md §"Neo4j Integration").
- [ ] During consolidation (Task 22), upsert entities/edges to Neo4j asynchronously after the SQLite graph.
- [ ] Vector index in Neo4j for enrichment-side similarity (optional).

## Files
- `go/internal/graph/{neo4j.go,entity.go,relationship.go,enrichment.go,neo4j_test.go}`

## Acceptance Criteria
- [ ] With Neo4j up: entities/edges persist; Cypher traversal + enrichment lookup work.
- [ ] With Neo4j down/absent: service runs SQLite-only without errors (NFR-24).
- [ ] Consolidation mirrors graph data to Neo4j without blocking the hot path.
- [ ] Edge/label vocabulary matches Tasks 16/20.

## Notes
SQLite graph stays authoritative for the search hot path; Neo4j is the enrichment/analytics layer. Keep writes async so Neo4j latency never blocks capture or search.
