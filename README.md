# medha — agent memory

Persistent, long-term memory for AI coding agents. Captures what Claude Code does, compresses it, and surfaces relevant context at the start of each prompt.

## How it works

```
Claude Code (hooks) → POST /observe → privacy filter → dedup → PostgreSQL
                                                              ↓
                                              async compression (Python)
                                                              ↓
                                    PgFTS + vector index + graph index
                                                              ↓
                              UserPromptSubmit hook ← recall-summary
```

At session end, a consolidation pipeline distils observations into long-term memories. A nightly decay job (Ebbinghaus model) evicts weak memories over time.

---

## For developers connecting their project to medha

If medha is already running (your team has a shared instance, or you ran `medha_dev_setup.sh`), connect any Claude Code project in one command:

```bash
# From the medha repo
./bin/connect.sh /path/to/your/project

# Or from inside your project, pointing at the medha repo's bin/
/path/to/medha/bin/connect.sh
```

`connect.sh` will prompt for three things:

| Prompt | Default | What it does |
|--------|---------|--------------|
| medha API URL | `http://localhost:3111` | Where the Go service is running |
| medha secret | (blank) | `AGENTMEMORY_SECRET` bearer token |
| Project name | `basename` of the target dir | Stable ID for your project's memories |

It then:
1. Writes `.env.mcp` into your project root (picked up by hooks automatically)
2. Copies hooks and slash commands into `.claude/`
3. Wires hook events into `.claude/settings.json`
4. Registers the MCP server: `claude mcp add agent-mem --transport http <url>:3114/mcp`

Restart Claude Code in that directory. The hooks fire automatically from then on.

### Skip prompts (CI / scripted)

```bash
AGENTMEMORY_URL=http://192.168.2.91:3111 \
AGENTMEMORY_SECRET=your-secret \
AGENTMEMORY_PROJECT=myapp \
./bin/connect.sh /path/to/your/project
```

### What gets installed

| File | Purpose |
|------|---------|
| `.claude/hooks/agentmem-tool-hook` | `PostToolUse` → sends tool events as observations |
| `.claude/hooks/agentmem-session-end-hook` | `Stop` → triggers consolidation pipeline |
| `.claude/hooks/agentmem-notify-hook` | `Notification` → captures compaction summaries |
| `.claude/hooks/agentmem-recall-hook` | `UserPromptSubmit` → injects relevant memories |
| `.claude/hooks/agentmem-md-procedural-hook` | `Edit/Write` on `.md` → re-seeds procedural memory |
| `.claude/hooks/agentmem-seed-procedural` | CLI: seeds CLAUDE.md sections as procedural memory |
| `.env.mcp` | `AGENTMEMORY_URL`, `AGENTMEMORY_SECRET`, `AGENTMEMORY_PROJECT` |

### Project naming

`AGENTMEMORY_PROJECT` is the namespace for all memories from that repo. Hooks
read it from `.env.mcp` first; if absent they derive it from `git remote + branch`.
Setting it explicitly gives you a stable, human-readable name across branch switches.

---

## For medha contributors (running the stack)

### Prerequisites

- Docker + docker compose v2
- `jq`, `curl`, `openssl`
- A running [Bifrost](https://github.com/maximhq/bifrost) gateway (LLM proxy)
- PostgreSQL reachable from the containers

### First-time setup

```bash
./medha_dev_setup.sh
```

This will:
1. Prompt for Bifrost URL, LLM model, Postgres connection, Neo4j toggle
2. Generate `AGENTMEMORY_SECRET` (`openssl rand -hex 32`)
3. Write `.env` (full service config) and `.env.mcp` (hook config + service config for the MCP container)
4. Start the stack: `docker compose -f docker-compose.local.yml up -d --build`
5. Wait for `GET /agentmemory/health` to return 200
6. Connect the medha repo to itself via `bin/connect.sh`
7. Seed `CLAUDE.md` as procedural memory

```bash
# Re-run is safe (idempotent). Force config overwrite:
./medha_dev_setup.sh --force

# Use a pre-built image instead of building from source:
./medha_dev_setup.sh --pull <sha>
```

### Environment files

| File | Used by |
|------|---------|
| `.env` | `docker-compose.local.yml` Go + Python services |
| `.env.mcp` | `docker-compose.local.yml` MCP container; hooks walking up the directory tree |

`.env.mcp` is `.env` plus `AGENTMEMORY_URL=http://localhost:3111`.

### Local development (no Docker)

```bash
make setup       # go mod download + uv sync --all-extras
make run-py      # Python FastAPI on :5000
make run-go      # Go API on :3111, viewer on :3113
make test        # go test ./... -race + pytest
make lint        # golangci-lint + ruff + mypy
```

Integration tests require a live Postgres:

```bash
POSTGRES_TEST_HOST=localhost go test ./... -race
```

### Key env vars

| Var | Default | Notes |
|-----|---------|-------|
| `PORT` | `3111` | Go API |
| `VIEWER_PORT` | `3113` | WebSocket viewer |
| `AGENTMEMORY_SECRET` | (empty) | Bearer token; empty disables auth |
| `BIFROST_URL` | — | Required; LLM gateway |
| `LLM_MODEL` | (empty) | e.g. `deepseek/deepseek-v4-pro` |
| `EMBEDDING_MODEL` | (empty) | If unset, local hashing fallback |
| `POSTGRES_*` | see `.env.example` | |
| `NEO4J_ENABLED` | `false` | Optional; degrades gracefully (ADR-0003) |
| `QUEUE_BACKEND` | `memory` | `rabbitmq` for prod (ADR-0001) |

Full list in [CLAUDE.md](./CLAUDE.md) and [`.env.example`](./.env.example).

---

## Stack

| Component | Tech |
|-----------|------|
| Go API | Go 1.26.3, Chi, PostgreSQL, PgFTS + vector + graph hybrid search |
| Python sidecar | Python 3.14.5, FastAPI — NLP extraction, LLM compression, embeddings |
| State | PostgreSQL (primary) + Neo4j (optional, ADR-0003) |
| Async | In-memory queue (dev) or RabbitMQ (prod) — ADR-0001 |
| MCP | Streamable HTTP, port 3114 — 30+ tools |
| Search | PgFTS + vector + graph → RRF → provenance boost → Cohere reranker |

## Repository layout

```
medha-api/          Go service (cmd/{api,mcp,worker}, internal/*)
medha-extraction/   Python service (medha/*, tests/)
bin/
  connect.sh        Connect any project to a running medha instance
  install.sh        Low-level: copy hooks + wire settings.json
  agentmem-*        Hook scripts installed into .claude/hooks/
  commands/         Slash commands installed into .claude/commands/
medha_dev_setup.sh  Dev setup: configure + start the stack
docs/
  ADRs/             Architecture Decision Records
  api/openapi.yaml  REST API contract
deploy/             Docker compose overrides, prod config
```

## Documentation

- [Development guide](./docs/DEVELOPMENT.md)
- [Deployment guide](./docs/DEPLOYMENT.md)
- [Architecture decisions](./docs/ADRs/)
- [REST API contract](./docs/api/openapi.yaml)
- [Full architecture](./agent_mem.md)
