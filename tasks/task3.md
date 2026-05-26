# Task 3: Python service skeleton (FastAPI + config + health)

- **Milestone**: M0 — Scaffolding
- **Priority**: P0
- **Depends on**: Task 1
- **Tech**: Python 3.14.5
- **Maps to**: PRD FR-40, NFR-9, NFR-19; agent_mem.md §"Python Service"

## Objective
Stand up the FastAPI service with typed settings, structured logging, and health/metrics endpoints — the shell for extraction, compression, summarization, and embeddings.

## Scope & Steps
- [ ] Create `py/agent_mem/api.py`: FastAPI app on `:5000` with lifespan handler for model/provider warmup.
- [ ] Add `py/agent_mem/config.py`: `pydantic-settings` `Settings` (PORT, ANTHROPIC/OPENAI/GEMINI keys, EMBEDDING_PROVIDER, AGENTMEMORY_AUTO_COMPRESS, WIKIPEDIA_RATE_LIMIT, DIFFBOT_API_KEY, LOG_LEVEL).
- [ ] Add `GET /health` returning `{status, up_to_date, model_version}`.
- [ ] Add `GET /metrics` returning Prometheus-formatted text (placeholder counters).
- [ ] Add `py/agent_mem/utils/logging.py`: structured JSON logging configured at startup.
- [ ] Add `py/agent_mem/utils/retry.py`: exponential-backoff decorator for external calls.
- [ ] Add `py/agent_mem/utils/validators.py`: input validation helpers.
- [ ] Define base Pydantic models in `py/agent_mem/models/` (`observation.py`, `entity.py`, `relationship.py`, `compressed.py`) matching agent_mem.md schemas.
- [ ] Pin runtime deps in `pyproject.toml`: `fastapi`, `uvicorn[standard]`, `pydantic`, `pydantic-settings`, `httpx`, `prometheus-client`.

## Files
- `py/agent_mem/{__init__.py,api.py,config.py}`
- `py/agent_mem/utils/{logging.py,retry.py,validators.py}`
- `py/agent_mem/models/{observation.py,entity.py,relationship.py,compressed.py}`

## Acceptance Criteria
- [ ] `uvicorn agent_mem.api:app` starts on `:5000`; `curl :5000/health` returns `200`.
- [ ] Missing optional LLM keys do NOT prevent startup (degraded mode per NFR-9).
- [ ] `/metrics` returns valid Prometheus exposition format.
- [ ] Pydantic models import cleanly and round-trip sample JSON from agent_mem.md.

## Notes
Heavy NLP imports (spaCy/GLiNER) are deferred to Task 19; do not import them here to keep startup fast and the base image slim.
