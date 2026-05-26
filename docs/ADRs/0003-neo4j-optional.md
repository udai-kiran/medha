# ADR-0003: Neo4j is optional; SQLite-only mode is first-class

- **Status**: Accepted
- **Date**: 2026-05-26
- **Related**: PRD NFR-24, FR-20; agent_mem.md §"Key Design Decisions #2"

## Context

Neo4j powers entity enrichment and deep graph traversal. But:

- It is heavy to operate (Java VM, separate process, ~1 GB RSS minimum).
- Many users (solo developers, CI sandboxes, hobby deployments) cannot or do
  not want to run it.
- The hot path (capture, BM25 + vector search) does not require Neo4j —
  SQLite alone can store entities + simple BFS-2 edges for those operations.

We must avoid making Neo4j a hard dependency.

## Decision

- Neo4j is **optional**. Service startup is gated on `NEO4J_ENABLED` and a
  reachable URI; absence triggers a "degraded but healthy" health status
  rather than a startup failure.
- All graph operations are routed through a `GraphStore` interface:
  - `SQLiteGraphStore` (always present) — covers entity CRUD, BFS-2 traversal.
  - `Neo4jGraphStore` (optional) — adds enrichment, deep traversal, Cypher.
- The Docker Compose stack has a `lightweight` profile that omits Neo4j.
- The Go service must never panic or 500 on Neo4j unavailability — degrade.

## Consequences

- A single binary + SQLite is a valid deployment shape.
- Some FRs (entity enrichment FR-31/32, deep graph queries) are unavailable
  in lightweight mode; `/health` reports component status so callers know.
- Test matrix grows by one (lightweight vs. full); covered in Task 34.
