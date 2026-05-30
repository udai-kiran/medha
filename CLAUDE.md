# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# First-time setup
cp .env.example .env
make setup        # go mod download + uv sync --all-extras

# Build
make build        # Go binaries → medha-api/bin/

# Test
make test         # go test ./... -race -cover  +  pytest --cov
cd medha-api && go test ./internal/state/... -race                              # single package
cd medha-api && go test ./... -run TestObserve -race                            # single test by name
cd medha-api && go test ./... -coverprofile=cov.out && go tool cover -html=cov.out  # HTML coverage
cd medha-extraction && uv run pytest tests/test_extraction.py::test_name -v

# Reset local state
rm -rf medha-api/bin medha-extraction/.venv data/ && make setup

# Lint
make lint         # golangci-lint + ruff + mypy

# Run locally (two terminals)
make run-go       # API :3111, viewer :3113
make run-py       # Python FastAPI :5000
make worker       # async consolidation consumer (optional; API uses in-memory queue by default)

# Docker
make compose-up           # full stack: Go + Python + Neo4j + RabbitMQ
docker compose --profile postgres up   # includes embedded PostgreSQL
make compose-light        # Go + Python only (no Neo4j)
make compose-down

# Smoke check
curl -s http://localhost:3111/agentmemory/health | jq
curl -s http://localhost:5000/health | jq
```

## PostgreSQL (primary datastore)

The Go service requires PostgreSQL. Schema migrations run automatically on startup.

For local development without Docker, create the database first:
```bash
createdb medha && createuser medha
psql medha -c "ALTER USER medha WITH PASSWORD 'medha-password';"
```

**Integration tests** are skipped unless `POSTGRES_TEST_HOST` is set (`testutil.OpenStore` in `medha-api/internal/testutil/db.go` handles this automatically). To run them:
```bash
POSTGRES_TEST_HOST=localhost go test ./... -race
```

## Architecture

**Two services, one system:**

| Service | Directory | Port | Role |
|---------|-----------|------|------|
| Go API | `medha-api/` | :3111 (API), :3113 (viewer) | Ingestion, search, state, MCP, auth |
| Python sidecar | `medha-extraction/` | :5000 | NLP extraction, LLM compression, embeddings |

The Go service handles all hot-path operations; Python is called async (via in-memory queue or RabbitMQ) for CPU/LLM-heavy work. If Python is unreachable, Go falls back to synthetic compression and skips vector indexing.

### Go service internals (`medha-api/internal/`)

- **`state/`** — PostgreSQL backend. `schema.go` holds forward-only migrations. `kv.go` provides a 34-scope key-value layer on top of the `kv` table. The store is opened once in `cmd/api/main.go` and injected everywhere.
- **`api/`** — Chi router wired in `router.go`. All routes live under `/agentmemory` with Bearer auth + rate limiting (120 req/min). The `/internal` sub-router is Python→Go callback only. `RouterDeps` struct injects all collaborators.
- **`search/`** — Three independent indexes: BM25 (keyword), vector (cosine similarity via Python `/embed`), graph (entity BFS). `Hybrid` in the same package fuses results using Reciprocal Rank Fusion (k=60) with a per-session diversity cap of 3.
- **`consolidation/`** — `Pipeline` runs the SessionEnd DAG: fetch observations → POST `/summarize` to Python → POST `/extract` to Python → distil memories → persist. Best-effort: individual steps fail without aborting the rest. `DecayEngine` applies Ebbinghaus decay (`strength *= rate^daysOld`; hard-evict below threshold) on a nightly scheduler.
- **`dedup/`** — SHA-256 rolling 5-minute window per session to drop duplicate observations.
- **`privacy/`** — Fail-closed filter applied before any persistence. Strips `<private>…</private>` blocks, redacts API keys/JWTs/key=value secrets, and removes ANSI codes. Sets `HasSecrets` flag on the observation so downstream enrichment is skipped (FR-9).
- **`mcp/`** — MCP server using `modelcontextprotocol/go-sdk`. Streamable HTTP transport (spec 2025-06-18). Tool handlers are thin wrappers over the same store/search functions the REST API uses. Mounted at `/agentmemory/mcp` in the API and served standalone on port 3114 via `cmd/mcp`.
- **`graph/`** — Optional Neo4j Bolt driver. The service runs in degraded mode when `NEO4J_ENABLED=false` (ADR-0003).
- **`telemetry/`** — Prometheus metrics (counters: observations, dedup hits, privacy redactions, consolidation runs, LLM/embed calls; histograms: search latency). Served at `/metrics`.
- **`viewer/`** — WebSocket hub at :3113; broadcasts live observations to the dashboard. SSE stream also available at `GET :3113/events`.

### Python service internals (`medha-extraction/medha/`)

- `api.py` — FastAPI app exposing `/extract`, `/compress`, `/summarize`, `/embed`, `/enrich`, `/health`.
- `extraction/` — Heuristic entity extractor with optional LLM fallback (`pipeline.py`).
- `compression/` — `LLMCompressor` (XML-structured output) with `synthetic_compressor.py` fallback.
- `summarization/` — `SessionSummarizer` (LLM or synthetic fallback).
- `llm/` — Bifrost client factory. `build_llm_client()` returns an `OpenAICompatibleClient` pointed at `BIFROST_URL/v1`, or `None` (→ synthetic fallback) when `BIFROST_URL` is unset.
- `embedding/` — `pick_embedder()` uses Bifrost when `EMBEDDING_MODEL` is set, local hashing otherwise. Guards the vector-index fingerprint on startup.
- `enrichment/` — Wikipedia lookup with SQLite cache.

### Data flow summary

1. Agent fires `POST /agentmemory/observe` → privacy filter → dedup check → store `RawObservation` → enqueue compression job.
2. Worker consumes → calls Python `/compress` → Python calls Go `POST /internal/observation/{id}/compressed` → BM25 + vector indexed.
3. Agent fires `SessionEnd` hook → consolidation pipeline → `SessionSummary` + `Memory` rows created.
4. Nightly decay job evicts memories whose strength drops below `DECAY_EVICTION_THRESHOLD`.

### Key ADRs

| ADR | Decision |
|-----|----------|
| ADR-0001 | Queue backend: `memory` (dev default) or `rabbitmq` (prod), controlled by `QUEUE_BACKEND` |
| ADR-0002 | Ebbinghaus decay constants are config-driven (`DECAY_RATE_PER_DAY`, `DECAY_EVICTION_THRESHOLD`) |
| ADR-0003 | Neo4j is optional; service degrades gracefully when disabled |
| ADR-0004 | All REST routes under `/agentmemory/` base path |
| ADR-0005 | MCP tool surface: small, tested set delegating to REST logic |
| ADR-0006 | Extraction defaults to heuristic (no heavy NLP dependency) |

## Key env vars

| Var | Default | Notes |
|-----|---------|-------|
| `POSTGRES_HOST` | `localhost` | |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_USER` | `medha` | |
| `POSTGRES_PASSWORD` | `medha-password` | |
| `POSTGRES_DB` | `medha` | |
| `POSTGRES_SSLMODE` | `disable` | |
| `AGENTMEMORY_SECRET` | (empty) | Bearer token; empty disables auth (dev) |
| `QUEUE_BACKEND` | `memory` | `rabbitmq` for prod |
| `NEO4J_ENABLED` | `false` | Optional graph enrichment |
| `DECAY_RATE_PER_DAY` | `0.95` | Ebbinghaus base rate |
| `DECAY_EVICTION_THRESHOLD` | `0.1` | Hard-evict memories below this strength |
| `PYTHON_SERVICE_URL` | `http://localhost:5000` | Go→Python calls |
| `BIFROST_URL` | **(required)** | Bifrost endpoint, e.g. `http://192.168.2.91:8080`; service won't start without it |
| `BIFROST_API_KEY` | (empty) | Optional; Bifrost may not require auth |
| `LLM_MODEL` | (empty) | Model for all LLM stages, e.g. `anthropic/claude-3-5-haiku` |
| `COMPRESS_MODEL` | (empty) | Per-stage override; falls back to `LLM_MODEL` |
| `SUMMARIZE_MODEL` | (empty) | Per-stage override; falls back to `LLM_MODEL` |
| `EXTRACT_MODEL` | (empty) | Per-stage override; falls back to `LLM_MODEL` |
| `EMBEDDING_MODEL` | (empty) | If set, Bifrost is used for embeddings; if unset, local hashing fallback. Changing it means reindex |
| `LOG_LEVEL` | `info` | Go service log level; `debug` enables verbose tracing |

## MCP configuration

The MCP server uses **Streamable HTTP transport** (MCP spec 2025-06-18) on port 3114.

```bash
# Run via Docker (recommended)
docker run -d -p 3114:3114 --env-file .env.mcp ghcr.io/udai-kiran/agent-mem-mcp:latest

# Configure Claude Code
claude mcp add agent-mem --transport http http://localhost:3114/mcp
```

The same MCP surface is also available at `GET|POST /agentmemory/mcp` on the main API (port 3111).

## Privacy convention for agents

Wrap any text that must never be persisted in `<private>…</private>` tags before sending it through `POST /observe`. The privacy filter strips these blocks before any DB write, ahead of the secret-pattern redaction pass.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `/observe` returns 401 | `AGENTMEMORY_SECRET` set, no bearer | Add `Authorization: Bearer …` |
| `/smart-search` returns empty | Compression job hasn't run | Ensure `make worker` (or in-process queue) is running |
| Vector results empty | Python `/embed` unreachable | `make run-py` |
| Neo4j down warning | Expected unless Neo4j is running | ADR-0003 — degraded mode is safe |
| Integration tests skipped | `POSTGRES_TEST_HOST` not set | `POSTGRES_TEST_HOST=localhost go test ./... -race` |