# Task 18: POST /smart-search + single-mode search

- **Milestone**: M2 — Compression & Search
- **Priority**: P0
- **Depends on**: Task 17
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-16, FR-17, FR-18, FR-19; agent_mem.md §"Phase 2: Search"

## Objective
Expose the hybrid search engine over HTTP, plus a context-formatting endpoint for injection-ready recall.

## Scope & Steps
- [ ] `internal/api/search.go`: `POST /agentmemory/smart-search {project, query, limit, mode}` → ranked results with `{observationId, type, title, relevance, snippet, sessionId, timestamp, concepts}`.
- [ ] `GET /agentmemory/search` (simple keyword) and `GET /agentmemory/recall` (memories) convenience endpoints.
- [ ] `GET /agentmemory/context?sessionId=` → formatted, injection-ready context string (FR-19).
- [ ] Input validation + sane defaults (limit, mode=hybrid).
- [ ] Reinforce strength on retrieved memories (hook for Task 24; stub-safe).
- [ ] Update `docs/api/openapi.yaml` with `/agentmemory/smart-search`, `/agentmemory/search`, `/agentmemory/recall`, `/agentmemory/context`.

## Files
- `go/internal/api/{search.go,context.go,search_test.go}`

## Acceptance Criteria
- [ ] `POST /smart-search` returns ranked results end-to-end (capture → compress → index → search) in an integration test.
- [ ] `mode` switches between hybrid and single-path search.
- [ ] `/context` returns a formatted string suitable for prompt injection.
- [ ] Invalid input → `400` with clear message.

## Notes
This closes the M2 loop and is the primary value-delivery endpoint (PRD UC1). Wire retrieval-reinforcement behind an interface so Task 24 can implement decay/reinforcement without changing this handler.
