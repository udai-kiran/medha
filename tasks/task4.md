# Task 4: Docker Compose local stack

- **Milestone**: M0 — Scaffolding
- **Priority**: P0
- **Depends on**: Task 2, Task 3
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD NFR-21, NFR-24; agent_mem.md §"Development Setup", §"Deployment"

## Objective
One-command local bring-up of the full stack: Go service, Python service, Neo4j, and the message queue — with health-gated startup ordering.

## Scope & Steps
- [ ] Write `go/Dockerfile`: multi-stage build on `golang:1.26.3`, final stage `scratch`/`distroless`, single static binary.
- [ ] Write `py/Dockerfile`: base `python:3.14.5-slim`, install via `uv`, non-root user.
- [ ] Write root `docker-compose.yml` with services: `go` (`:3111`,`:3113`), `py` (`:5000`), `neo4j` (`:7687`,`:7474`), `rabbitmq` (`:5672`,`:15672`).
- [ ] Add `depends_on` with `condition: service_healthy`; define healthchecks for each service.
- [ ] Mount named volumes for Neo4j data and the SQLite file; pass env from `.env`.
- [ ] Add a `lightweight` compose profile that omits Neo4j (validates NFR-24 SQLite-only mode).
- [ ] Document `make compose-up` / `make compose-down` flows in root README.

## Files
- `go/Dockerfile`, `py/Dockerfile`
- `docker-compose.yml`
- `deploy/docker-compose.prod.yml` (production overrides skeleton)

## Acceptance Criteria
- [ ] `docker compose up` brings all services healthy; both `/health` endpoints return `200`.
- [ ] `docker compose --profile lightweight up` starts Go+Python without Neo4j and the Go service reports degraded-but-healthy.
- [ ] Go image final size is small (no toolchain in final layer); Python image runs as non-root.
- [ ] Stopping with `docker compose down` preserves Neo4j volume data across restarts.

## Notes
Keep prod compose minimal here; full K8s manifests are Task 33/35 scope.
