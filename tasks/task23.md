# Task 23: 4-tier memory model & storage

- **Milestone**: M3 — Consolidation & Decay
- **Priority**: P0
- **Depends on**: Task 22
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-26, FR-24; agent_mem.md §"Phase 4", FEATURE_ANALYSIS.md §2 & §6

## Objective
Model the four memory tiers (Working/Episodic/Semantic/Procedural) with explicit lifecycle rules and the storage/transition logic between them.

## Scope & Steps
- [ ] Define tier metadata on records: tier enum + `createdAt`, `strength`, `lastRetrieved`, `isLatest`.
- [ ] Tier assignment rules:
  - Working: `RawObservation` (24h TTL).
  - Episodic: `CompressedObservation` + `SessionSummary` (7d TTL → archive).
  - Semantic: extracted facts/entities/preferences (decay).
  - Procedural: workflows/decision patterns (decay + manual "always keep").
- [ ] `internal/consolidation/tiers.go`: transition logic (Working→Episodic on compression; Episodic→Semantic/Procedural on consolidation).
- [ ] Persist tier + strength fields in state (extend Task 6 schema via new migration).
- [ ] Memory CRUD: create from consolidation, mark `isLatest`, track `sourceObservationIds`.
- [ ] Supersession: a newer memory supersedes an older one (set `isLatest=false`).

## Files
- `go/internal/consolidation/tiers.go`
- `go/internal/state/migration_*.go` (new migration for tier/strength fields)
- `go/internal/consolidation/tiers_test.go`

## Acceptance Criteria
- [ ] Records carry correct tier + lifecycle fields.
- [ ] Consolidation promotes observations into Semantic/Procedural memories.
- [ ] Supersession flips `isLatest` correctly; search can filter to latest.
- [ ] Migration applies cleanly to an existing DB.

## Notes
Tiers are a classification + lifecycle policy over the same stores, not separate databases. Decay execution itself is Task 24.
