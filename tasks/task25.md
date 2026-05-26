# Task 25: Full REST API surface (Go)

- **Milestone**: M4 — REST & MCP
- **Priority**: P0
- **Depends on**: Task 18, Task 23
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-40, FR-19, FR-20, FR-21; agent_mem.md §"REST API Endpoints", §"Phase 4"

## Objective
Complete the documented REST surface beyond capture/search: session, observation, memory, graph, profile, and consolidation routes.

## Scope & Steps
- [ ] All routes use the `/agentmemory` base path (ADR-0004) — no unprefixed routes.
- [ ] Session: `POST /agentmemory/session/start`, `POST /agentmemory/session/end`, `GET /agentmemory/sessions`, `GET /agentmemory/session/:id`.
- [ ] Observation: `GET /agentmemory/observations`, `GET /agentmemory/observation/:id`, `GET /agentmemory/file-history`, `GET /agentmemory/timeline`.
- [ ] Memory: `POST /agentmemory/remember`, `POST /agentmemory/forget`, `GET /agentmemory/memories`, `GET /agentmemory/recall`.
- [ ] Graph: `GET /agentmemory/graph`, `GET /agentmemory/relations`, `GET /agentmemory/entity/:id` (depth + confidence params).
- [ ] Diagnostics: `GET /agentmemory/profile` (top concepts/files/patterns), `GET /agentmemory/health`, `GET /agentmemory/metrics`.
- [ ] Consolidation: `POST /agentmemory/consolidate`, `GET /agentmemory/lessons`, `POST /agentmemory/auto-forget`.
- [ ] Consistent error envelope, pagination, and auth (Task 33 enforces auth; wire the middleware hook here).
- [ ] Update `docs/api/openapi.yaml` with every route added here (the spec is maintained incrementally from Task 2 onward, not first-authored in Task 35).

## Files
- `go/internal/api/{sessions.go,observations.go,memories.go,graph.go,profile.go,consolidate.go}`
- `go/internal/api/api_test.go`

## Acceptance Criteria
- [ ] All listed routes return correct shapes with realistic data after an end-to-end session.
- [ ] Pagination + filtering work on list endpoints.
- [ ] `/profile` aggregates top concepts/files from stored data.
- [ ] Routes are covered by handler tests.

## Notes
This is breadth work; keep handlers thin and delegate to state/search/consolidation packages. Don't reimplement logic that lives in earlier tasks.
