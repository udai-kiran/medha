# Deployment guide

Three modes are supported. Pick the smallest that meets the requirement.

## Deployment shapes

| Shape | Persistence | External services | When to pick |
|-------|-------------|-------------------|--------------|
| **Lightweight (NFR-24)** | SQLite | None (or just Python) | Solo dev, CI sandbox, hobby use. |
| **Full** | SQLite + Neo4j | RabbitMQ, Neo4j, Python | Team, project memory, multi-agent orchestration. |
| **Kubernetes** | Same as Full | Add k8s manifests | Self-host at scale. |

ADR-0003 governs Neo4j optionality; ADR-0001 governs queue backend.

## Lightweight via Docker Compose

```bash
cp .env.example .env
docker compose -f docker-compose.yml -f deploy/docker-compose.lightweight.yml up -d --build
curl http://localhost:3111/agentmemory/health
```

What you get:
- Go API on `:3111` (REST + MCP-over-HTTP at `/agentmemory/mcp`).
- Viewer on `:3113`.
- Python sidecar on `:5000`.
- SQLite at `/data` volume; no Neo4j.

The Go service reports `degraded` on `/health` because Neo4j is disabled —
this is expected and explicitly handled per ADR-0003.

## Full stack via Docker Compose

```bash
cp .env.example .env       # then edit: AGENTMEMORY_SECRET, NEO4J_PASSWORD, *_API_KEY
docker compose up -d --build
```

Adds Neo4j (`:7687`) and RabbitMQ (`:5672`). Healthchecks gate startup;
`docker compose ps` should show all `(healthy)` within a minute.

### Production overrides

`deploy/docker-compose.prod.yml` adds restart policies + resource caps:

```bash
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml up -d
```

## Kubernetes (skeleton)

k8s manifests aren't shipped — the docker-compose files are the supported
deploy unit today. The path from compose to k8s is short:

- `services.go` → `Deployment` (3 replicas) + `Service` + `Ingress`.
- `services.py` → `Deployment` (2 replicas) + `Service`.
- `services.neo4j` → `StatefulSet` (1 replica, persistent volume).
- `services.rabbitmq` → `StatefulSet` (3 replicas, clustering).

Add manifests under `deploy/k8s/` when first deploying — see the layout
sketch in [`agent_mem.md`](../agent_mem.md) §"Kubernetes (Optional)".

## Operational concerns

### Backup

The Go service exposes `state.Backup(dst)` (Task 33). A simple cron:

```bash
docker exec agent-mem-go /app/agent-mem-api -backup /backups/$(date +%Y%m%d).db
# (CLI flag — wire in main.go before relying on this)
```

For now, snapshot the SQLite file directly:

```bash
docker exec agent-mem-go sh -c 'sqlite3 /data/agentmemory.db ".backup /tmp/snap.db"'
docker cp agent-mem-go:/tmp/snap.db ./backups/
```

### Restore

```bash
docker compose down
docker run --rm -v sqlite_data:/data -v $(pwd)/backups:/in alpine \
  cp /in/snap.db /data/agentmemory.db
docker compose up -d
```

### Observability

- Prometheus scrapes `:3111/metrics` (no `/agentmemory` prefix).
- Structured JSON logs on stdout — pipe to your log aggregator.
- OTEL traces (when `OTEL_EXPORTER_OTLP_ENDPOINT` is set) — wire scaffolding
  lives in `internal/telemetry`; full instrumentation is incremental.

### Health probes

| Endpoint | Purpose |
|----------|---------|
| `GET /health` | Liveness — always 200 unless the process is dying. |
| `GET /agentmemory/health` | Same, with component details (Neo4j, queue). |
| `GET /metrics` | Prometheus exposition. |
| `GET :3113/health` | Viewer hub status (subscriber count). |

### Rate limiting

`api.NewRateLimiter(120, time.Minute)` is the default — 120 req/min per
bearer token (or per IP when auth is off). Override in `main.go` per
environment.

## Configuration secrets

Generate the bearer token:

```bash
AGENTMEMORY_SECRET=$(openssl rand -hex 32) >> .env
```

For LLM/embedding keys, prefer your platform's secrets manager. Mount
`.env` from the secret store rather than committing values.

## Capacity planning

Per the PRD §7.4 budget:

- Go service: < 100 MB RSS under nominal load.
- Python service: < 500 MB RSS (heavier when spaCy/GLiNER models are loaded).
- Storage: ~2 KB per compressed observation, ~5 KB per memory.
  - 100K observations + 10K memories ≈ 250 MB.
- Neo4j: 1-2 GB RSS (heap-bound; tune `NEO4J_dbms_memory_heap_max__size`).
- RabbitMQ: 256-512 MB RSS for typical traffic.
