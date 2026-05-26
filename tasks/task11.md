# Task 11: Synthetic compression path (Python)

- **Milestone**: M1 — Capture pipeline
- **Priority**: P0
- **Depends on**: Task 3
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-10, FR-11, NFR-9; agent_mem.md §"Phase 1B" (synthetic fallback)

## Objective
Provide a zero-LLM compression path that always works without an API key, producing a `CompressedObservation` via tokenization and regex file extraction.

## Scope & Steps
- [ ] `agent_mem/compression/synthetic_compressor.py`: build narrative `f"{toolName} | {stringify(toolInput)} | {truncate(toolOutput,400)}"`.
- [ ] Regex file extraction (e.g., paths ending in common code extensions) → `files[]`.
- [ ] `infer_type(toolName)` mapping → observation `type` (file_read, file_edit, command, search, ...).
- [ ] Set `concepts=[]`, `facts=[]`, `importance=5`, `confidence=0.3` (low, no LLM).
- [ ] `agent_mem/compression/validator.py`: shape/quality checks on output.
- [ ] Expose `POST /compress` in `api.py` that uses synthetic path when `AGENTMEMORY_AUTO_COMPRESS=false` or no LLM key.
- [ ] Return `CompressedObservation` Pydantic model; unit tests for type inference and file regex.

## Files
- `py/agent_mem/compression/{__init__.py,synthetic_compressor.py,validator.py}`
- `py/tests/test_compression.py`

## Acceptance Criteria
- [ ] `POST /compress` returns a valid `CompressedObservation` with no LLM configured.
- [ ] File paths in tool I/O are correctly extracted into `files[]`.
- [ ] `type` inference covers the common tool names.
- [ ] Confidence is 0.3 and importance 5 for synthetic output.

## Notes
This is the reliability floor for NFR-9 — it must never require network or keys. The LLM path (Task 13) layers on top and falls back here on timeout/error.
