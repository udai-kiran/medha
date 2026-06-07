# ADR-0004: REST base path and versioning

- **Status**: Accepted (updated v1.0.0 — 2026-06-02)
- **Date**: 2026-05-26
- **Related**: PRD §6 ("Route convention"), agent_mem.md §"REST API Endpoints"

## Context

The PRD documents short route names like `/observe`, `/smart-search`, but
all public routes ship under a shared prefix in the running service. With v1.0.0
we formalised versioning by adopting an explicit `/v1/agentmemory` prefix so
the API version is visible in every URL.

## Decision

- All public, agent-facing REST routes live under `/v1/agentmemory`.
- Internal, service-to-service callbacks live under `/internal`.
- `/health`, `/agentmemory/health`, `/metrics`, and viewer routes are exposed
  without a version prefix so generic infrastructure tooling (load balancers,
  probes) reaches them without needing version awareness.
- The OpenAPI document (`docs/api/openapi.yaml`) reflects this convention and
  marks routes as stable or `x-stability: experimental`.

## Consequences

- Breaking change from v0.x: all `/agentmemory/…` client URLs must add `/v1`.
  Hook scripts, MCP configs, and curl examples have been updated accordingly.
- Future versions use `/v2/agentmemory` with `/v1` kept as a compatibility shim.
- Auth + rate-limiting middleware is applied via the single Chi subrouter on
  `/v1/agentmemory`, keeping `/health` and `/metrics` open for probes.
