# Task 26: MCP server (stdio + HTTP proxy)

- **Milestone**: M4 — REST & MCP
- **Priority**: P0
- **Depends on**: Task 25
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-41; agent_mem.md §"MCP Server", FEATURE_ANALYSIS.md §10

## Objective
Expose the memory system to agents over MCP: tools mapping to REST capabilities, resources, prompts, and slash skills — via stdio with an HTTP proxy fallback.

## Scope & Steps
- [ ] `internal/mcp/server.go`: MCP server over stdio transport; HTTP proxy mode for clients without stdio.
- [ ] `internal/mcp/tools.go`: tool definitions covering read (recall, search, smart_search, get_context, get_entity, get_graph, file_history, timeline), write (remember, store_message, add_entity/preference/fact, create_relationship), consolidation (consolidate, auto_forget), and diagnostics (profile, health, metrics). Each tool calls the corresponding internal handler.
- [ ] `internal/mcp/resources.go`: resources (status, profile, memories, graph) — at least 6.
- [ ] `internal/mcp/prompts.go`: prompts (≥3) for common recall/consolidate flows.
- [ ] `internal/mcp/skills.go`: slash skills `/recall`, `/remember`, `/session-history`, `/forget`.
- [ ] JSON schema for each tool's params; validation + error mapping.
- [ ] Document client config snippet (Claude Code / Cursor) in README.

## Files
- `go/internal/mcp/{server.go,tools.go,resources.go,prompts.go,skills.go,mcp_test.go}`

## Acceptance Criteria
- [ ] An MCP client lists and successfully calls core tools (recall, smart_search, remember).
- [ ] Resources and prompts are discoverable.
- [ ] Slash skills work end-to-end.
- [ ] HTTP proxy mode works when stdio is unavailable.

## Notes
Reuse the handler logic from Task 25 — MCP tools should be thin adapters over the same internal services, not a parallel implementation.
