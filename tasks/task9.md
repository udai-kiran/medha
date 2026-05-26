# Task 9: Deduplication (5-min SHA-256 window)

- **Milestone**: M1 — Capture pipeline
- **Priority**: P0
- **Depends on**: Task 8
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-3, NFR-8; agent_mem.md §"Phase 1A" (dedup), §"dedup/"

## Objective
Drop duplicate observations within a 5-minute rolling window per session so storage and indexes stay clean.

## Scope & Steps
- [ ] `internal/dedup/window.go`: compute key `SHA-256(sessionId + toolName + canonicalJSON(toolInput))`.
- [ ] Maintain a per-session rolling window (5 min) of seen hashes with timestamps.
- [ ] `internal/dedup/store.go`: in-memory map with periodic eviction + optional SQLite persistence for restart durability.
- [ ] Expose `Deduper` interface consumed by Task 8: `Seen(obs) (bool, error)`.
- [ ] On duplicate: handler returns `202 {deduplicated:true}` and skips persistence/enqueue.
- [ ] Make window duration configurable (default 5 min).
- [ ] Ensure canonical JSON (stable key ordering) so semantically identical inputs hash equally.

## Files
- `go/internal/dedup/{window.go,store.go,dedup_test.go}`

## Acceptance Criteria
- [ ] Identical observation within 5 min → `202`, not stored twice.
- [ ] Same observation after window expiry → stored normally.
- [ ] Dedup accuracy ≥ 99% in a test with shuffled key ordering (NFR-8).
- [ ] Eviction prevents unbounded memory growth (verified in test).

## Notes
Canonicalize `toolInput` before hashing — key order and whitespace must not produce false negatives.
