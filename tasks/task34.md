# Task 34: Test suite — unit, integration, load, chaos

- **Milestone**: M6 — Production Hardening
- **Priority**: P0
- **Depends on**: Tasks 8, 18, 22 (and broadly all prior)
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD NFR-1, NFR-6, NFR-7, NFR-8, NFR-14; agent_mem.md §"Testing Strategy"

## Objective
Comprehensive automated testing across both services and the full pipeline, plus load and chaos tests validating the PRD's performance and reliability targets.

## Scope & Steps
- [ ] Go unit tests: validation, dedup window, privacy filter, BM25, vector cosine, RRF fusion, decay (raise coverage ≥ 70%).
- [ ] Python unit tests: spaCy/GLiNER extraction, LLM compression (mocked), dedup, embeddings, enrichment (mocked).
- [ ] Integration: `observation pipeline` (hook→store→compress→search), `consolidation pipeline` (SessionEnd→memory), `graph queries` (Cypher) — run against the compose stack in CI (complete Task 5's placeholder).
- [ ] Load: `k6` script simulating 1K observations/session and 100 concurrent agents; assert p50/p95/p99 search latency (NFR-1) and capture success (NFR-14).
- [ ] Chaos: kill Python/Neo4j/queue mid-flight; assert graceful degradation (NFR-9) and recovery.
- [ ] Recall benchmark harness for the R@10 KPI (dataset choice = PRD OQ4).

## Files
- `go/**/*_test.go`, `py/tests/**`
- `tests/integration/*`, `tests/load/agent_sim.js`, `tests/chaos/*`

## Acceptance Criteria
- [ ] CI runs unit + integration on every PR; coverage gate met.
- [ ] Load test meets NFR-1 latency targets at 10K observations.
- [ ] Chaos tests confirm degraded-mode behavior and recovery.
- [ ] Recall harness produces an R@10 number against the chosen dataset.

## Notes
Wire integration tests into the `integration-test.yml` workflow from Task 5. Use the in-memory queue backend (Task 12) for fast unit/integration runs; reserve RabbitMQ for the compose-based integration job.
