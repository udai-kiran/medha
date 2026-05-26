# Task 19: Entity extraction pipeline (Python)

- **Milestone**: M3 — Consolidation & Decay
- **Priority**: P1
- **Depends on**: Task 11
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-13, FR-9; agent_mem.md §"Phase 3 Step 3", reference/DESIGN.md extraction pipeline

## Objective
Implement multi-stage entity extraction (spaCy → GLiNER → LLM fallback) producing POLE+O-typed entities with confidence and provenance.

## Scope & Steps
- [ ] `agent_mem/extraction/spacy_extractor.py`: fast NER; load model lazily at first use.
- [ ] `agent_mem/extraction/gliner_extractor.py`: zero-shot extraction for types spaCy misses.
- [ ] `agent_mem/extraction/llm_extractor.py`: LLM fallback for low-confidence/ambiguous spans.
- [ ] `agent_mem/extraction/types.py`: POLE+O enum (PERSON, OBJECT, LOCATION, EVENT, ORGANIZATION) + code subtypes (FILE, FUNCTION, LIBRARY).
- [ ] `agent_mem/extraction/merger.py`: merge multi-stage results, dedup spans, keep highest-confidence type + provenance (`extractorName`).
- [ ] `agent_mem/extraction/pipeline.py`: orchestrate stages with thresholds; skip entities flagged sensitive (FR-9).
- [ ] `POST /extract {observations[]} -> {entities[], relationships[]}` (relationships filled by Task 20).
- [ ] Tests with fixture texts covering each stage.

## Files
- `py/agent_mem/extraction/{__init__.py,pipeline.py,spacy_extractor.py,gliner_extractor.py,llm_extractor.py,types.py,merger.py,entity.py}`
- `py/tests/test_extraction.py`

## Acceptance Criteria
- [ ] `/extract` returns typed entities with confidence and `extractorName` provenance.
- [ ] spaCy handles high-confidence cases without invoking the LLM (cost control).
- [ ] Sensitive-flagged content is not extracted/enriched.
- [ ] Models load lazily; service startup stays fast.

## Notes
Pin spaCy/GLiNER model versions in `pyproject.toml` and bake them into the Python image (Task 4) so cold start doesn't download at runtime. Verify spaCy/GLiNER wheels support Python 3.14.5; if a wheel lags, document the constraint here.
