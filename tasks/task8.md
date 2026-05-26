# Task 8: POST /observe + validation

- **Milestone**: M1 — Capture pipeline
- **Priority**: P0
- **Depends on**: Task 7, Task 10 (privacy filter must exist and be wired before persistence)
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-1, FR-2, FR-4, FR-5, NFR-2; agent_mem.md §"Phase 1A: Observation Capture"

## Objective
Implement the capture hot path: validate a hook payload, persist a `RawObservation`, update session counters, and return immediately (non-blocking; compression is async).

## Scope & Steps
- [ ] `internal/api/observe.go`: `POST /agentmemory/observe` handler.
- [ ] Validate required fields and `hookType` enum (reuse `HookPayload.Validate()`); return `400` with field-level errors on failure.
- [ ] On `SessionStart`: create/ensure `Session` row.
- [ ] **Privacy filter FIRST (Task 10):** run the real fail-closed filter on the payload before anything is stored or enqueued; if the filter errors, reject/redact — never persist the raw payload.
- [ ] **Dedup check (Task 9 interface):** if duplicate within the window, return `202 {deduplicated:true}` and stop — no persistence, no enqueue.
- [ ] Build `RawObservation` from the *filtered* payload (generate `obs-` id, timestamp, modality detection); persist via state layer.
- [ ] Detect and extract inline images (`data:image/...`) into a stored blob/ref; set modality `image|mixed` (FR-5).
- [ ] Increment `session.observationCount`; update `updatedAt`.
- [ ] Emit a viewer broadcast event (interface stub; real impl in Task 28).
- [ ] Enqueue compression job (interface stub; real impl in Task 12).
- [ ] Return `201 {observationId, compressing:true, compressed:false}` in < 50 ms (NFR-2).
- [ ] Update `docs/api/openapi.yaml` with the `/agentmemory/observe` route (request, `201`/`202`/`400` responses, error envelope).

## Files
- `go/internal/api/observe.go`, `go/internal/api/observe_test.go`

## Acceptance Criteria
- [ ] Valid payload → `201` with `observationId`; row present in SQLite.
- [ ] Malformed payload → `400` with actionable message.
- [ ] `SessionEnd` payload accepted and routed to consolidation enqueue (stub ok until Task 22).
- [ ] Handler latency p99 < 50 ms in a local benchmark with stubbed async deps.
- [ ] No code path persists or enqueues an observation that has not passed the privacy filter (fail-closed); verified by a test injecting a secret and asserting it never reaches storage or the queue.

## Notes
Privacy filtering (Task 10) is a hard prerequisite and runs **before** persistence/enqueue — it must be a real fail-closed filter, not a stub, or secrets could reach storage and the LLM. Dedup (Task 9) may start as an interface stub that degrades to "no dedup", since a missed duplicate is not a security risk.
