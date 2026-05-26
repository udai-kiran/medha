# ADR-0004: REST base path is `/agentmemory`

- **Status**: Accepted
- **Date**: 2026-05-26
- **Related**: PRD §6 ("Route convention"), agent_mem.md §"REST API Endpoints"

## Context

The PRD documents short route names like `/observe`, `/smart-search`, but
all public routes ship under a shared prefix in the running service. Without
this written down, individual task authors may pick different prefixes (e.g.
`/v1/`, `/api/`, `/mem/`), producing an inconsistent surface.

## Decision

- All public, agent-facing REST routes live under `/agentmemory`.
- Internal, service-to-service callbacks live under `/internal`.
- `/health`, `/metrics`, and viewer routes are exposed at the root (no prefix)
  so generic infrastructure tooling (load balancers, probes) reaches them.
- The OpenAPI document (`docs/api/openapi.yaml`) reflects this convention;
  every route-adding task (8, 18, 25, 26, 33) updates the document; Task 35
  finalizes it.

## Consequences

- One place to bolt on a future API version (e.g. `/agentmemory/v2`).
- Trivial to apply a single Chi router subrouter for auth + rate limiting
  to the entire public surface.
- The MCP server can rewrite the prefix transparently for the HTTP proxy.
