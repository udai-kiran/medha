# Task 10: Privacy filter (secrets, <private>, ANSI)

- **Milestone**: M1 — Capture pipeline
- **Priority**: P0
- **Depends on**: Task 2 (standalone component; must land before Task 8 wires the capture path)
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-6, FR-7, FR-8, NFR-10; agent_mem.md §"Phase 1A" (privacy), FEATURE_ANALYSIS.md §9

## Objective
Strip secrets and private content from observations before any persistence or downstream LLM call (defense in depth).

## Scope & Steps
- [ ] `internal/privacy/regex.go`: patterns for API keys (OpenAI `sk-`, Anthropic `sk-ant-`, AWS, GitHub `ghp_`, generic `password=|token=|secret=|api[_-]?key=`).
- [ ] `internal/privacy/filter.go`: redact matches to `***REDACTED***`; remove entire `<private>…</private>` blocks; expose `Filter(raw) (filtered, foundSecrets bool)`.
- [ ] `internal/privacy/ansi.go`: strip ANSI escape sequences.
- [ ] Apply filter in the capture path (Task 8) BEFORE storing `RawObservation` and BEFORE enqueueing compression.
- [ ] Mark observations that contained secrets so downstream extraction/enrichment can skip them (feeds FR-9).
- [ ] Build a test corpus of secret formats; assert zero leakage.

## Files
- `go/internal/privacy/{filter.go,regex.go,ansi.go,privacy_test.go}`
- `go/internal/privacy/testdata/secrets_corpus.txt`

## Acceptance Criteria
- [ ] Known secret formats are redacted before storage (0 leaks against the corpus) (NFR-10).
- [ ] `<private>` blocks are fully removed, including multi-line.
- [ ] ANSI codes stripped from tool output.
- [ ] Filtering adds < 5 ms to the capture path for typical payloads.

## Notes
Order matters: strip `<private>` blocks first, then redact secret patterns, then strip ANSI. Keep the corpus easy to extend as new providers appear.
