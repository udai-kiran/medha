# agent_mem

Persistent, long-term memory for AI coding agents — hybrid Go + Python.

- **Architecture**: see [`agent_mem.md`](./agent_mem.md)
- **Product requirements**: see [`PRD.md`](./PRD.md)
- **Implementation tasks**: see [`tasks/`](./tasks/)
- **Decisions**: see [`docs/ADRs/`](./docs/ADRs/)

## Stack

| Component       | Tech                              |
|-----------------|-----------------------------------|
| API service     | Go 1.26.3, Chi router, SQLite     |
| Extraction svc  | Python 3.14.5, FastAPI, spaCy/GLiNER |
| State           | SQLite (always) + Neo4j (optional, ADR-0003) |
| Async           | RabbitMQ (prod) or in-memory (dev) — ADR-0001 |
| Entrypoints     | REST `/agentmemory/*`, MCP stdio + HTTP proxy |

## Quick start (Docker Compose)

```bash
cp .env.example .env
make compose-up           # full stack: Go + Python + Neo4j + RabbitMQ
# or
make compose-light        # lightweight: Go + Python only (no Neo4j)
```

Health checks:

```bash
curl http://localhost:3111/agentmemory/health
curl http://localhost:5000/health
open  http://localhost:3113     # viewer (Task 28)
```

## Local development (without Docker)

### Prerequisites

- **Go 1.26.3** — pinned in `.tool-versions` and `mise.toml`; install via [mise](https://mise.jdx.dev/) or [asdf](https://asdf-vm.com/).
- **Python 3.14.5** — same.
- **uv** — Python package manager. Install: `curl -LsSf https://astral.sh/uv/install.sh | sh`.

### Setup

```bash
make setup    # go mod download + uv sync --all-extras
make lint     # golangci-lint + ruff + mypy
make test     # go test + pytest
```

### Run services

```bash
make run-go   # API on :3111, viewer placeholder on :3113
make run-py   # Python service on :5000
make worker   # async job consumer (appears in Task 12)
```

## Repository layout

```
.
├── medha-api/                   # Go service (cmd/api, cmd/worker, internal/*)
├── medha-extraction/                   # Python service (agent_mem/*, tests/)
├── docs/
│   ├── ADRs/             # Architecture Decision Records
│   └── api/openapi.yaml  # REST API contract (grown per task)
├── deploy/               # K8s manifests, prod compose overrides
├── tasks/                # Implementation task files
├── reference/            # Source designs (DESIGN.md, LOW_LEVEL_DESIGN.md)
├── PRD.md
├── agent_mem.md
├── FEATURE_ANALYSIS.md
└── docker-compose.yml    # Local stack
```

## Status

All 35 implementation tasks (M0 → M6) are complete:

- **M0** scaffolding (toolchain, skeletons, Docker Compose, CI).
- **M1** capture pipeline (observe → privacy filter → dedup → SQLite).
- **M2** compression + hybrid search (BM25 + vector + graph fused via RRF).
- **M3** consolidation + Ebbinghaus decay (4-tier memory model, nightly job).
- **M4** REST API surface + MCP server (stdio + HTTP proxy) + optional Neo4j.
- **M5** WebSocket viewer dashboard + Prometheus metrics + JSON logs.
- **M6** entity enrichment (Wikipedia), orchestration primitives
  (Actions/Leases/Routines/Signals), team sharing + audit, auth + rate
  limiting + backup/restore, end-to-end test suite + docs.

See [`tasks/`](./tasks/) for the per-task acceptance criteria,
[`docs/ADRs/`](./docs/ADRs/) for design decisions, and
[`docs/api/openapi.yaml`](./docs/api/openapi.yaml) for the REST contract.

## Documentation

- [Development guide](./docs/DEVELOPMENT.md)
- [Deployment guide](./docs/DEPLOYMENT.md)
- [Architecture decisions](./docs/ADRs/)
- [REST API contract](./docs/api/openapi.yaml)
