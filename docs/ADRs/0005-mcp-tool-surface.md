# ADR-0005: MCP exposes a small curated tool surface

- **Status**: Accepted
- **Date**: 2026-05-26
- **Related**: PRD FR-41; agent_mem.md §"MCP Server (53 tools)"

## Context

agent_mem.md proposes exposing all 124 REST endpoints as MCP tools (~53 in
the cited counting). MCP tool catalogues are not free — every tool consumes
tokens in every conversation that lists them, and most agents pick from
the top half-dozen.

## Decision

Ship a **curated surface** of seven MCP tools that cover the
day-to-day actions an agent actually needs:

- `smart-search`
- `recall` / `remember` / `forget`
- `session-history`
- `status`

Plus the four MCP "slash skills" called out in agent_mem.md (`/recall`,
`/remember`, `/session-history`, `/forget`) as prompt templates.

Agents that need the full surface use the REST API directly (or the MCP
HTTP proxy, which is the same REST surface re-wrapped).

## Consequences

- Token budget per conversation stays small (tool list ~1 KB).
- The MCP server's test surface is tight enough to verify exhaustively.
- A future task can expand the catalogue if usage shows demand for it.
- The REST → MCP mapping stays "explicit register" rather than codegen,
  which keeps the boundary visible.
