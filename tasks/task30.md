# Task 30: Entity enrichment (Wikipedia/Diffbot/Wikidata)

- **Milestone**: M6 — Production Hardening
- **Priority**: P2
- **Depends on**: Task 19, Task 27
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-31, FR-32; agent_mem.md §"Python Service" (enrichment), reference/DESIGN.md enrichment

## Objective
Enrich extracted entities with external knowledge (description, URLs, IDs, image) via rate-limited background calls, cached locally.

## Scope & Steps
- [ ] `agent_mem/enrichment/wikipedia.py`, `wikidata.py`, `diffbot.py`: fetch enrichment per entity type.
- [ ] `agent_mem/enrichment/cache.py`: local SQLite cache keyed by entity name+type with TTL.
- [ ] Respect `WIKIPEDIA_RATE_LIMIT` and Diffbot quotas; exponential backoff (reuse `utils/retry.py`).
- [ ] `POST /enrich {entity} -> {enriched_description, wikipedia_url, wikidata_id, image_url}`.
- [ ] Skip entities flagged sensitive (FR-9) — never send them to external APIs.
- [ ] Go side: consolidation enqueues enrichment as a low-priority background job; results written to Neo4j entity fields (Task 27).
- [ ] Tests with mocked HTTP responses.

## Files
- `py/agent_mem/enrichment/{__init__.py,wikipedia.py,wikidata.py,diffbot.py,cache.py}`
- `py/tests/test_enrichment.py`

## Acceptance Criteria
- [ ] `/enrich` returns enrichment fields for a known entity; cache hit avoids re-fetch.
- [ ] Rate limits respected; backoff on 429.
- [ ] Sensitive entities are never enriched.
- [ ] Enrichment runs in background without blocking consolidation.

## Notes
Enrichment is best-effort and async — failures must never fail consolidation. Diffbot/Wikidata are optional and key-gated.
