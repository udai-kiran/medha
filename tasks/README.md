# agent_mem — Implementation Tasks

Fine-grained, implementable tasks derived from [`../agent_mem.md`](../agent_mem.md) and [`../PRD.md`](../PRD.md).

**Toolchain (pinned):**
- **Go**: 1.26.3
- **Python**: 3.14.5

Each task file is self-contained: objective, ordered steps, files to touch, acceptance criteria, and traceability to PRD requirement IDs (FR-/NFR-) and `agent_mem.md` sections.

### Source documents (canonical vs reference)

- **Canonical** (build to these): [`../PRD.md`](../PRD.md) and [`../agent_mem.md`](../agent_mem.md) — they define the target system as **Go + Python** with SQLite + Neo4j + a job queue.
- **Reference only** (background / prior art): [`../reference/DESIGN.md`](../reference/DESIGN.md) (Neo4j Agent Memory) and [`../reference/LOW_LEVEL_DESIGN.md`](../reference/LOW_LEVEL_DESIGN.md) (agentmemory — a Node.js/iii, KV-only design with no external DB). Where these conflict with the canonical docs (language, stores, dependencies), the canonical docs win.

### Resolved decisions (lock before coding)

- **OQ2 — job queue:** RabbitMQ in production; an in-memory backend for dev/test; both behind a narrow `Queue` interface (Redis remains a future option). Seeded as ADR-0001 in Task 1.
- **OQ1 — decay constants:** config-driven (defaults: decay 0.95/day, evict < 0.1) so they can be tuned pre-GA. ADR-0002.
- **Neo4j is optional:** SQLite-only "lightweight" mode is a first-class deployment (NFR-24). ADR-0003.

### Conventions

- **REST base path:** all public routes are served under `/agentmemory` (e.g. `/agentmemory/observe`, `/agentmemory/smart-search`, `/agentmemory/session/start`). The PRD uses short names (`/observe`) for brevity; tasks use the full path. ADR-0004.
- **OpenAPI:** `docs/api/openapi.yaml` is seeded in Task 2 and updated by every route-adding task; Task 35 only finalizes it.

## Task Index

| # | Task | Milestone | Priority | Depends on |
|---|------|-----------|----------|------------|
| 1 | [Repo & toolchain bootstrap](./task1.md) | M0 Scaffolding | P0 | — |
| 2 | [Go service skeleton (Chi + config + health)](./task2.md) | M0 | P0 | 1 |
| 3 | [Python service skeleton (FastAPI + config + health)](./task3.md) | M0 | P0 | 1 |
| 4 | [Docker Compose local stack](./task4.md) | M0 | P0 | 2, 3 |
| 5 | [CI pipelines (GitHub Actions)](./task5.md) | M0 | P0 | 2, 3 |
| 6 | [SQLite schema, migrations & state layer](./task6.md) | M1 Capture | P0 | 2 |
| 7 | [Core domain models (Go)](./task7.md) | M1 | P0 | 6 |
| 8 | [POST /observe + validation](./task8.md) | M1 | P0 | 7, 10 |
| 9 | [Deduplication (5-min SHA-256 window)](./task9.md) | M1 | P0 | 8 |
| 10 | [Privacy filter (secrets, &lt;private&gt;, ANSI)](./task10.md) | M1 | P0 | 2 |
| 11 | [Synthetic compression path (Python)](./task11.md) | M1 | P0 | 3 |
| 12 | [Async queue integration](./task12.md) | M2 Compression+Search | P0 | 6, 11 |
| 13 | [LLM compression worker (Python)](./task13.md) | M2 | P1 | 11, 12 |
| 14 | [BM25 index & search (Go)](./task14.md) | M2 | P0 | 6 |
| 15 | [Embeddings + vector index](./task15.md) | M2 | P0 | 3, 6 |
| 16 | [Graph index & storage (Go)](./task16.md) | M2 | P0 | 6 |
| 17 | [RRF fusion + hybrid orchestrator](./task17.md) | M2 | P0 | 14, 15, 16 |
| 18 | [POST /smart-search + single-mode search](./task18.md) | M2 | P0 | 17 |
| 19 | [Entity extraction pipeline (Python)](./task19.md) | M3 Consolidation+Decay | P1 | 11 |
| 20 | [Relationship extraction (Python)](./task20.md) | M3 | P1 | 19 |
| 21 | [Summarization (Python)](./task21.md) | M3 | P1 | 13 |
| 22 | [SessionEnd consolidation orchestrator (Go)](./task22.md) | M3 | P0 | 12, 19, 21 |
| 23 | [4-tier memory model & storage](./task23.md) | M3 | P0 | 22 |
| 24 | [Ebbinghaus decay + nightly job](./task24.md) | M3 | P1 | 23 |
| 25 | [Full REST API surface (Go)](./task25.md) | M4 REST+MCP | P0 | 18, 23 |
| 26 | [MCP server (stdio + HTTP proxy)](./task26.md) | M4 | P0 | 25 |
| 27 | [Neo4j graph integration](./task27.md) | M4 | P1 | 16, 20 |
| 28 | [Real-time viewer & dashboard](./task28.md) | M5 Viewer+Observability | P1 | 8, 23 |
| 29 | [Observability: OTEL + metrics + logs](./task29.md) | M5 | P0 | 2, 3 |
| 30 | [Entity enrichment (Wikipedia/Diffbot)](./task30.md) | M6 Hardening | P2 | 19, 27 |
| 31 | [Orchestration primitives (Actions/Leases/Routines/Signals)](./task31.md) | M6 | P1 | 25 |
| 32 | [Team namespacing, sharing & audit](./task32.md) | M6 | P1 | 25 |
| 33 | [Auth, rate limiting, graceful shutdown, backup/restore](./task33.md) | M6 | P0 | 25 |
| 34 | [Test suite: unit, integration, load, chaos](./task34.md) | M6 | P0 | many |
| 35 | [Documentation: API ref, deploy, dev, ADRs](./task35.md) | M6 | P1 | many |

## Suggested Execution Order

1. **Foundation (M0):** Tasks 1–5 — must complete before anything else.
2. **Capture (M1):** Tasks 6–11 — the ingestion hot path.
3. **Search (M2):** Tasks 12–18 — indexing + hybrid retrieval.
4. **Memory (M3):** Tasks 19–24 — extraction, consolidation, decay.
5. **Surface (M4):** Tasks 25–27 — full API/MCP + graph.
6. **Ops (M5):** Tasks 28–29 — viewer + observability.
7. **Hardening (M6):** Tasks 30–35 — enrichment, orchestration, teams, security, tests, docs.

Parallelizable: within M2, tasks 14/15/16 are independent; within M3, Python tasks 19/20/21 can proceed alongside Go tasks 22/23.

**Ordering constraint:** Task 10 (privacy filter) must be implemented and wired **fail-closed before** Task 8 persists or enqueues any observation — secret filtering happens before storage and before any external/LLM call (PRD NFR-10). Dedup (Task 9) may start as a stub since a missed dedup is not a security risk.
