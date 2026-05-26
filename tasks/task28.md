# Task 28: Real-time viewer & dashboard

- **Milestone**: M5 — Viewer & Observability
- **Priority**: P1
- **Depends on**: Task 8, Task 23
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-42, FR-43, UC8; agent_mem.md §"Real-Time Viewer & Observability (:3113)"

## Objective
Provide a real-time viewer on `:3113`: WebSocket streams for observations/memories/graph plus a dashboard for sessions, memories, search, and the knowledge graph.

## Scope & Steps
- [ ] `internal/viewer/server.go`: HTTP+WebSocket server on `:3113` (gorilla/websocket or stdlib).
- [ ] `internal/viewer/stream.go`: pub/sub hub; implement the broadcast interface stubbed in Task 8 (observation/memory/graph events).
- [ ] `internal/viewer/dashboard.go`: serve a static HTML/JS dashboard:
  - Live observation stream.
  - Memory browser (filter by tier/type/strength).
  - Search explorer (calls `/smart-search`).
  - Session timeline.
  - Knowledge-graph view (nodes/edges).
- [ ] Backpressure handling: drop/coalesce events for slow clients.
- [ ] Health panel surfacing component status + latency (data from Task 29).

## Files
- `go/internal/viewer/{server.go,stream.go,dashboard.go}`
- `go/internal/viewer/static/` (HTML/JS/CSS)

## Acceptance Criteria
- [ ] New observations/memories appear live in the dashboard via WebSocket.
- [ ] Search explorer returns ranked results interactively.
- [ ] Graph view renders entities + relationships.
- [ ] Slow clients don't stall the broadcast hub.

## Notes
Keep the frontend dependency-light (vanilla JS or a tiny lib) so it ships inside the Go binary via `embed`. UI changes should be verified in a browser before marking done (golden path + a slow-client edge case).
