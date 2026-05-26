# Task 6: SQLite schema, migrations & state layer

- **Milestone**: M1 — Capture pipeline
- **Priority**: P0
- **Depends on**: Task 2
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-4, FR-26, NFR-16; agent_mem.md §"State Management"

## Objective
Implement the SQLite-backed state layer: schema, versioned migrations, connection pooling, and a KV abstraction over the documented scopes.

## Scope & Steps
- [ ] Choose driver: `modernc.org/sqlite` (pure-Go, no cgo) to keep the scratch image static; document the choice.
- [ ] `internal/state/schema.go`: **core tables only** — `sessions`, `observations`, `memories`, `sessions_summary`, `graph_entities`, `graph_edges`, `audit_log`. Do NOT define BM25/vector index tables here; those migrations are owned by Tasks 14/15 (whose engine choices determine the schema).
- [ ] `internal/state/migration.go`: embedded, versioned migrations (`schema_version` table); run on startup; forward-only.
- [ ] `internal/state/sqlite.go`: open DB, set `WAL` mode, busy timeout, connection pool sizing.
- [ ] `internal/state/kv.go`: typed KV abstraction over the 34 scopes (e.g., `sessions:{project}`, `observations:{project}`, `memories:{project}`); namespaced key builder + CRUD.
- [ ] Add CRUD for sessions and observations used by Task 8.
- [ ] Add unit tests with a temp DB file.

## Files
- `go/internal/state/{schema.go,migration.go,sqlite.go,kv.go,kv_test.go}`

## Acceptance Criteria
- [ ] Fresh DB auto-migrates to latest version on startup; re-running is idempotent.
- [ ] WAL mode enabled; concurrent reads don't block on a single writer in tests.
- [ ] KV scope keys are correctly namespaced by project; CRUD round-trips.
- [ ] `go test ./internal/state/...` passes with `-race`.

## Notes
This task owns only the core observation/session/memory/graph schema. Search-index tables and their migrations live with the tasks that choose the engines — BM25 in Task 14, vector in Task 15 — so the schema follows the implementation decision rather than pre-committing it. Keep migrations additive; never edit a shipped migration — add a new one.
