# Task 22: SessionEnd consolidation orchestrator (Go)

- **Milestone**: M3 — Consolidation & Decay
- **Priority**: P0
- **Depends on**: Task 12, Task 19, Task 21
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-22, FR-23, FR-24, FR-25; agent_mem.md §"Phase 3: Consolidation"

## Objective
On SessionEnd, run the async consolidation DAG: fetch observations → summarize → extract entities/relationships → build graph → cluster → extract facts → persist + index.

## Scope & Steps
- [ ] `internal/consolidation/scheduler.go`: SessionEnd hook → enqueue `consolidate{sessionId}`; return `202` immediately.
- [ ] `internal/consolidation/pipeline.go`: orchestrate steps as a DAG with per-step error isolation:
  1. Fetch session observations from state.
  2. Call Python `/summarize` → `SessionSummary`.
  3. Call Python `/extract` → entities + relationships.
  4. Upsert into graph (Task 16 / Task 27).
  5. Call Python `/cluster` + `/extract-facts` → `Memory[]`.
  6. Persist `SessionSummary` + memories; index via BM25 + vector (Tasks 14/15).
  7. Update session status=completed, endedAt, summary.
- [ ] Idempotency: re-running consolidation for a session updates rather than duplicates.
- [ ] Partial-failure handling: a failed step logs + degrades (e.g., no entities) without aborting the whole pipeline.
- [ ] Emit metrics/traces per step (feeds Task 29).

## Files
- `go/internal/consolidation/{scheduler.go,pipeline.go,pipeline_test.go}`
- `go/internal/python/{client.go,extract.go,summarize.go}`

## Acceptance Criteria
- [ ] SessionEnd → `202`; async pipeline produces a `SessionSummary` + ≥1 memory for a non-trivial session.
- [ ] New memories are immediately searchable (BM25 + vector).
- [ ] Re-consolidation is idempotent.
- [ ] A simulated Python `/extract` failure still yields a summary (graceful degradation, NFR-9).
- [ ] Pipeline completes in 5–30 s for a typical session (NFR-4).

## Notes
This is the integration spine of M3. Keep each Python call behind the `internal/python` client with per-call timeouts and fallbacks.
