# ADR-0002: Memory decay constants are configuration

- **Status**: Accepted
- **Date**: 2026-05-26
- **Resolves**: PRD `OQ1`
- **Related**: PRD FR-28..FR-30, NFR-5; agent_mem.md §"Phase 4: Decay & Auto-Forget", §"Key Design Decisions #5"

## Context

The PRD specifies Ebbinghaus-style decay (`strength *= rate^daysOld`) for
Semantic and Procedural memories with eviction below a strength threshold.
The exact constants (decay rate, eviction threshold, review band) cannot be
tuned correctly until we observe real usage — but we cannot block GA waiting
for that data.

## Decision

Treat decay parameters as **configuration**, not constants:

| Variable                    | Default | Meaning                                            |
|-----------------------------|---------|----------------------------------------------------|
| `DECAY_RATE_PER_DAY`        | `0.95`  | Multiplier applied per elapsed day                 |
| `DECAY_EVICTION_THRESHOLD`  | `0.1`   | Hard-evict memories whose strength falls below     |
| `DECAY_REVIEW_LOW`          | `0.1`   | Lower bound of the optional review band            |
| `DECAY_REVIEW_HIGH`         | `0.3`   | Upper bound of the optional review band            |

Defaults match the PRD's quoted formula (`0.95^daysOld`, evict < `0.1`).
The decay job (Task 24) reads these at startup and re-reads on config reload.

## Consequences

- Operators can tune memory longevity per deployment without code changes.
- Pre-GA we can run two profiles (aggressive vs. lenient) against a benchmark
  set to choose better defaults — see PRD `OQ1`.
- No breaking change later if we adopt a different functional form (e.g.
  exponential vs. logarithmic) — the env vars become provider-specific.
