# Development guide

This doc covers running agent_mem locally for development. Production
deployment lives in [`DEPLOYMENT.md`](./DEPLOYMENT.md).

## Prerequisites

- **Go 1.26.3** — pinned in `.tool-versions` / `mise.toml`. Install via
  [mise](https://mise.jdx.dev/) or [asdf](https://asdf-vm.com/).
- **Python 3.14.5** — same.
- **uv** — Python package manager. `curl -LsSf https://astral.sh/uv/install.sh | sh`.
- **Docker + docker compose v2** — for the full stack (Neo4j, RabbitMQ).

## First-time setup

```bash
cp .env.example .env
make setup       # go mod download + uv sync --all-extras
make lint        # golangci-lint + ruff + mypy
make test        # go test + pytest
```

## Run services

The fastest local loop is to skip Docker and run the two binaries directly.

### One terminal: Python sidecar

```bash
make run-py
# uvicorn agent_mem.api:app on :5000
```

### Another terminal: Go API

```bash
make run-go
# Go API on :3111, viewer on :3113
```

### Optional: async worker

The API process publishes compression/consolidation jobs to an in-memory
queue (ADR-0001 default). To run a separate consumer process:

```bash
make worker
```

### Full stack (Docker)

```bash
make compose-up           # Go + Python + Neo4j + RabbitMQ
make compose-light        # Go + Python only (NFR-24 lightweight mode)
make compose-down
```

## Smoke check

```bash
curl -s http://localhost:3111/agentmemory/health | jq
curl -s http://localhost:5000/health | jq

# Capture an observation
curl -s -X POST http://localhost:3111/agentmemory/observe \
  -H 'Content-Type: application/json' \
  -d '{
        "hookType": "post_tool_use",
        "sessionId": "sess-dev-1",
        "project": "demo",
        "timestamp": "'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'",
        "data": {
          "tool_name": "read",
          "tool_input": {"file_path": "src/auth.ts"},
          "tool_output": "export function validateToken() {}"
        }
      }'

# Search
curl -s -X POST http://localhost:3111/agentmemory/smart-search \
  -H 'Content-Type: application/json' \
  -d '{"project":"demo","query":"validate token","mode":"bm25"}' | jq
```

## Layout

```
go/             Go service (cmd/api, cmd/worker, cmd/mcp, internal/*)
py/             Python service (agent_mem/*, tests/)
docs/
  ADRs/         Architecture Decision Records
  api/          OpenAPI spec
  *.md          Guides (this file, DEPLOYMENT, DESIGN background)
deploy/         Docker compose overrides, k8s manifests
tasks/          Implementation task files (background)
reference/      Source designs (DESIGN.md, LOW_LEVEL_DESIGN.md)
```

## Common operations

### Watch the live viewer

`open http://localhost:3113` after `make run-go`. Every `POST /observe`
appears in the live feed (Task 28). For a curl-driven check:

```bash
curl -N http://localhost:3113/events     # SSE stream
```

### Connect MCP from Claude Code

```bash
claude mcp add agent-mem -- go run ./cmd/mcp
# or compiled
claude mcp add agent-mem -- $(pwd)/go/bin/agent-mem-mcp
```

The MCP server exposes seven tools: `smart-search`, `recall`, `remember`,
`forget`, `session-history`, `status`, plus the standard MCP
introspection methods (`tools/list`, `resources/list`, `prompts/list`).

### Tighten lint

```bash
make lint                          # golangci-lint + ruff + mypy
cd go && go test ./... -race        # race detector (requires cgo)
cd go && go test ./... -coverprofile=cov.out && go tool cover -html=cov.out
```

### Reset state

```bash
rm -rf go/bin py/.venv data/
make setup
```

## Configuration knobs

Everything is env-var-driven, listed in [`.env.example`](../.env.example).
Highlights:

| Var                          | Default | Notes |
|------------------------------|---------|-------|
| `AGENTMEMORY_SECRET`         | (empty) | Bearer token; empty disables auth (dev). |
| `QUEUE_BACKEND`              | `memory` | `rabbitmq` for prod (ADR-0001). |
| `NEO4J_ENABLED`              | `false` | Optional (ADR-0003). |
| `DECAY_RATE_PER_DAY`         | `0.95`  | Ebbinghaus base (ADR-0002). |
| `DECAY_EVICTION_THRESHOLD`   | `0.1`   | Memories below this are hard-evicted. |
| `EMBEDDING_PROVIDER`         | `local` | `openai`, `gemini`, `voyage` available. |
| `LOG_LEVEL`                  | `info`  | `debug` for verbose tracing. |

## Where to look when something breaks

| Symptom | Likely culprit | Fix |
|---------|----------------|-----|
| `/observe` returns 401 | `AGENTMEMORY_SECRET` set, no bearer | Add `Authorization: Bearer …` |
| `/smart-search` returns empty | Compression hasn't run | Check `cmd/worker` is up |
| Vector results empty | Python `/embed` unreachable | `make run-py` |
| Neo4j down warning | Expected if `NEO4J_ENABLED=false` | ADR-0003 — degraded mode |
| Tests failing on Python 3.12 | `pydantic-settings` needs 3.13+ on certain features | Use 3.14.5 via mise/asdf |
