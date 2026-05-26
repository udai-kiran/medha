# Task 31: Orchestration primitives (Actions/Leases/Routines/Signals)

- **Milestone**: M6 — Production Hardening
- **Priority**: P1
- **Depends on**: Task 25
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-33, FR-34, FR-35, FR-36; agent_mem.md §"Future Extensions #1", FEATURE_ANALYSIS.md §8

## Objective
Add multi-agent coordination primitives: an Action DAG with a frontier, exclusive Leases, Routines, and inter-agent Signals.

## Scope & Steps
- [ ] Actions: `POST /actions` (create with dependencies), `GET /frontier` (unblocked actions + priority), `GET /next` (next action + context). DAG cycle detection.
- [ ] Leases: `POST /leases` acquire (actionId, agentId, durationMs) with expiry; renewal + release; prevent double-claim.
- [ ] Routines: `POST /routines`, `POST /routine/run` (workflow templates).
- [ ] Signals: `POST /signals/send` (target agent, message) + inbox `GET /signals` with delivery receipts.
- [ ] (P2) Link ReasoningTrace → Actions; crystallize step chains into a procedural `Memory` (FR-36).
- [ ] State tables for actions/leases/routines/signals (new migration).
- [ ] Concurrency-safe lease acquisition (atomic compare-and-set).

## Files
- `go/internal/api/{actions.go,leases.go,routines.go,signals.go}`
- `go/internal/models/action.go` (complete behavior left from Task 7)
- `go/internal/state/migration_*.go`

## Acceptance Criteria
- [ ] Action DAG respects dependencies; frontier returns only unblocked actions; cycles rejected.
- [ ] Two agents cannot hold the same lease; expiry frees it.
- [ ] Signals deliver with receipts.
- [ ] Routines run a templated sequence.

## Notes
This layer is optional for single-agent use (PRD NG3) — keep it isolated so it adds no overhead when unused.
