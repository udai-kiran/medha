# Task 35: Documentation — API ref, deployment, dev guide, ADRs

- **Milestone**: M6 — Production Hardening
- **Priority**: P1
- **Depends on**: Tasks 25, 26, 29, 33 (and broadly all prior)
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD §10, §13; agent_mem.md §"Phase 6" (documentation)

## Objective
Ship the documentation set needed for adoption and operation: API reference, deployment guide, development guide, and Architecture Decision Records.

## Scope & Steps
- [ ] `docs/API.md` + `docs/api/openapi.yaml`: finalize the incrementally-maintained OpenAPI spec (seeded in Task 2, updated by every route-adding task) + render `API.md` from it + MCP tool catalog.
- [ ] `docs/DEPLOYMENT.md`: Docker Compose (dev + prod), Kubernetes manifests (`deploy/k8s/`), config reference (all env vars), scaling notes.
- [ ] `docs/DEVELOPMENT.md`: local setup with Go 1.26.3 + Python 3.14.5, running services without Docker, test commands.
- [ ] `docs/ARCHITECTURE.md`: condensed system overview linking to `agent_mem.md` + `PRD.md`.
- [ ] `docs/ADRs/`: expand/finalize the ADRs seeded in Task 1 (ADR-0001 queue/OQ2, ADR-0002 decay/OQ1, ADR-0003 Neo4j-optional, ADR-0004 endpoint convention) and add ADRs for decisions resolved during the build — benchmark dataset (OQ4), tenancy isolation (OQ5).
- [ ] `deploy/k8s/`: deployment manifests for Go (3 replicas), Python (2 replicas), Neo4j (StatefulSet), queue (StatefulSet), services + ingress.
- [ ] Update root `README.md` with quick start, architecture diagram, and links.

## Files
- `docs/{API.md,DEPLOYMENT.md,DEVELOPMENT.md,ARCHITECTURE.md}`
- `docs/ADRs/000N-*.md`
- `deploy/k8s/*.yaml`

## Acceptance Criteria
- [ ] A new developer can go from clone → running stack using only the docs.
- [ ] OpenAPI spec validates and matches the implemented routes.
- [ ] K8s manifests deploy the full stack in a test cluster.
- [ ] Each PRD open question (OQ1–OQ5) has a corresponding ADR capturing the decision.

## Notes
Keep `agent_mem.md` and `PRD.md` as the canonical design/requirements docs; `docs/` is operational reference, not a duplicate. Record decisions as ADRs so the rationale survives.
