# Task 21: Summarization (Python)

- **Milestone**: M3 — Consolidation & Decay
- **Priority**: P1
- **Depends on**: Task 13
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-23, FR-24; agent_mem.md §"Phase 3 Step 2 & 5–6"

## Objective
Summarize a session's observations into a `SessionSummary`, and cluster + extract reusable `Memory` objects (facts, patterns, decisions).

## Scope & Steps
- [ ] `agent_mem/summarization/session.py`: `POST /summarize {observations[]} -> SessionSummary{title, narrative, keyDecisions[], filesModified[], concepts[]}`.
- [ ] `agent_mem/summarization/conversation.py`: multi-turn conversation summary helper.
- [ ] `agent_mem/summarization/prompt_templates.py`: system prompts for summary, clustering, fact extraction.
- [ ] Clustering endpoint `POST /cluster {observations[]} -> groups[]` (semantic grouping via LLM).
- [ ] Fact extraction `POST /extract-facts {observations[]} -> memories[]` with memory `type`, `title`, `content`, `concepts`, `files`, `sourceObservationIds`.
- [ ] Enforce timeouts + fallback to a trivial extractive summary if LLM unavailable.
- [ ] Tests with a synthetic multi-observation session.

## Files
- `py/agent_mem/summarization/{__init__.py,session.py,conversation.py,prompt_templates.py}`
- `py/tests/test_summarization.py`

## Acceptance Criteria
- [ ] `/summarize` returns a well-formed `SessionSummary`.
- [ ] `/cluster` groups related observations; `/extract-facts` returns typed memories with provenance.
- [ ] LLM-unavailable path returns a degraded extractive summary, not an error.
- [ ] Token usage is bounded (truncate/batch large sessions).

## Notes
The Go consolidation orchestrator (Task 22) calls these endpoints. Keep each endpoint independently callable for testing and reuse.
