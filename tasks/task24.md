# Task 24: Ebbinghaus decay + nightly job

- **Milestone**: M3 — Consolidation & Decay
- **Priority**: P1
- **Depends on**: Task 23
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-27, FR-28, FR-29, FR-30, NFR-5; agent_mem.md §"Phase 4: Decay & Auto-Forget"

## Objective
Implement TTL eviction plus Ebbinghaus strength decay with retrieval reinforcement, run as a scheduled nightly job.

## Scope & Steps
- [ ] `internal/consolidation/decay.go`:
  - Working tier: hard-delete observations older than 24h.
  - Episodic tier: archive compressed observations older than 7d (Neo4j `:ArchivedObservation` label if present; else flag in SQLite).
  - Semantic/Procedural: `newStrength = strength * (decayConst ^ daysOld)` (default 0.95).
  - Hard-evict memories with `strength < 0.1`; surface 0.1–0.3 band for review (FR-30).
  - Remove evicted items from BM25 + vector + graph indexes.
- [ ] Retrieval reinforcement: implement the interface left by Task 18 — on recall, `strength += boost` (capped), update `lastRetrieved` (FR-29).
- [ ] Scheduler: nightly cron-like trigger (configurable hour); also `POST /agentmemory/consolidate` / `/auto-forget` manual triggers.
- [ ] "Always keep" flag bypasses decay for pinned procedural memories.
- [ ] Metrics: `{evicted, archived, decayed}` returned + emitted.

## Files
- `go/internal/consolidation/{decay.go,decay_test.go}`

## Acceptance Criteria
- [ ] Nightly job decays, archives, and evicts per the rules; index entries removed for evicted items.
- [ ] Retrieved memories gain strength and survive longer (reinforcement verified).
- [ ] "Always keep" memories never evicted.
- [ ] Job completes < 1 min for 100K memories (NFR-5).

## Notes
Decay constant (0.95) and threshold (0.1) are PRD OQ1 — make them config-driven so they can be tuned against real usage before GA.
