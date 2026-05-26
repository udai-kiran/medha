# Task 7: Core domain models (Go)

- **Milestone**: M1 — Capture pipeline
- **Priority**: P0
- **Depends on**: Task 6
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-1, FR-4, FR-10; agent_mem.md §"models/", FEATURE_ANALYSIS.md §"Data Model"

## Objective
Define the canonical Go structs shared across capture, search, and consolidation, with JSON tags matching the API contracts.

## Scope & Steps
- [ ] `models/hook.go`: `HookPayload{hookType, sessionId, project, cwd, timestamp, data}` with `hookType` enum (SessionStart, PostToolUse, PostToolFailure, UserPrompt, SessionEnd, ...).
- [ ] `models/observation.go`: `RawObservation` and `CompressedObservation` (type, title, subtitle, facts[], narrative, concepts[], files[], importance, confidence, modality).
- [ ] `models/memory.go`: `Memory` (type enum: pattern|preference|architecture|bug|workflow|fact; strength, isLatest, sourceObservationIds[]) and `SessionSummary`.
- [ ] `models/session.go`: `Session` lifecycle (status enum, observationCount, tags, summary).
- [ ] `models/action.go`: `Action`, `Lease` skeletons (fields only; behavior in Task 31).
- [ ] Add validation methods (`Validate() error`) on input-bound types.
- [ ] Add enum parsing/marshaling with round-trip tests.

## Files
- `go/internal/models/{hook.go,observation.go,memory.go,session.go,action.go,models_test.go}`

## Acceptance Criteria
- [ ] All structs marshal/unmarshal to the JSON shapes in agent_mem.md §"Data Flow".
- [ ] Enum types reject unknown values with clear errors.
- [ ] `Validate()` covers required fields for `HookPayload` and `RawObservation`.
- [ ] `go test ./internal/models/...` passes.

## Notes
Keep these pure data + validation; persistence logic stays in Task 6's state layer.
