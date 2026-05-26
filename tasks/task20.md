# Task 20: Relationship extraction (Python)

- **Milestone**: M3 — Consolidation & Decay
- **Priority**: P1
- **Depends on**: Task 19
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-14; agent_mem.md §"Phase 3 Step 4", FEATURE_ANALYSIS.md §5

## Objective
Extract typed, confidence-weighted relationships between entities, combining dependency/pattern heuristics with an LLM fallback.

## Scope & Steps
- [ ] `agent_mem/extraction/glirer_extractor.py`: relationship extraction (GLiREL or equivalent) over entity pairs.
- [ ] Pattern/dependency heuristics for code edges (DEPENDS_ON, IMPLEMENTS, EXPORTED_FROM) and social edges (WORKS_AT, MEMBER_OF, LOCATED_IN).
- [ ] LLM fallback for relations heuristics miss.
- [ ] Output edge set: `{source, target, type, confidence, sourceObsId}` using the merged vocabulary (incl. RELATED_TO, CONTRADICTS, SUPERSEDES, DERIVED_FROM).
- [ ] `agent_mem/models/relationship.py`: typed model.
- [ ] Extend `/extract` response to include `relationships[]`.
- [ ] Tests over fixtures with known relations.

## Files
- `py/agent_mem/extraction/glirer_extractor.py`
- `py/agent_mem/models/relationship.py`
- `py/tests/test_relationships.py`

## Acceptance Criteria
- [ ] `/extract` returns relationships with type + confidence + provenance.
- [ ] Code dependency edges detected from import/usage patterns.
- [ ] Confidence below threshold is dropped or flagged for LLM review.
- [ ] Vocabulary matches the Go graph edge types (Task 16) exactly.

## Notes
Keep the relationship vocabulary in one place shared conceptually with Task 16/27 so SQLite graph, Neo4j, and extraction stay aligned.
