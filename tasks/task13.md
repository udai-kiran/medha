# Task 13: LLM compression worker (Python)

- **Milestone**: M2 — Compression & Search
- **Priority**: P1
- **Depends on**: Task 11, Task 12
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-10, FR-12, FR-15, NFR-3; agent_mem.md §"Phase 1B: Async Compression"

## Objective
Add the LLM compression path: extract facts/concepts/narrative/importance via a configurable provider, with strict timeout and automatic fallback to synthetic.

## Scope & Steps
- [ ] `agent_mem/providers/llm.py`: provider factory (Anthropic/OpenAI/Gemini) with auto-detection from configured keys.
- [ ] `agent_mem/compression/llm_compressor.py`: build system+user prompt; call LLM with 60 s timeout; parse XML response (`<type>`, `<title>`, `<facts>`, `<narrative>`, `<concepts>`, `<files>`, `<importance>`).
- [ ] On timeout/error → fall back to `synthetic_compressor` (Task 11); set confidence accordingly.
- [ ] Vision: if modality is image, call vision model to produce `imageDescription` (FR-15).
- [ ] Wire `POST /compress` to choose LLM vs synthetic based on `AGENTMEMORY_AUTO_COMPRESS` + key availability.
- [ ] Go side: implement `POST /internal/observation/{id}/compressed` to persist the compressed result, then trigger indexing (Tasks 14–16) and viewer broadcast.
- [ ] `agent_mem/providers/cache.py`: cache identical compression requests.

## Files
- `py/agent_mem/compression/llm_compressor.py`
- `py/agent_mem/providers/{llm.py,cache.py}`
- `go/internal/api/internal_compressed.go`
- `py/tests/test_llm_compression.py`

## Acceptance Criteria
- [ ] With a key configured, `/compress` returns LLM-extracted facts/concepts/narrative; confidence ≥ 0.7.
- [ ] LLM timeout reliably falls back to synthetic without erroring the job.
- [ ] Go persists the compressed observation and kicks off indexing.
- [ ] Image observations get an `imageDescription`.

## Notes
Keep the XML-parsing tolerant (missing tags → defaults). Average LLM compression should be 2–3 s (NFR-3); enforce the timeout hard.
