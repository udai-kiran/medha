# Task 32: Team namespacing, sharing & audit

- **Milestone**: M6 — Production Hardening
- **Priority**: P1
- **Depends on**: Task 25
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-37, FR-38, FR-39, NFR-12; agent_mem.md §"Future Extensions", FEATURE_ANALYSIS.md §7

## Objective
Add multi-tenant scoping (global/team/user), explicit team memory sharing with a feed, and a governance audit trail for all mutations and shares.

## Scope & Steps
- [ ] Namespace hierarchy in the KV/state layer: `global:`, `team:{teamId}:`, `user:{userId}:` scopes.
- [ ] Permissions: observations private to creator (NFR-12); memories markable `private`/`team`.
- [ ] `POST /team/share {memoryIds[], teamId, mode}` (read/edit) → records share + emits feed event.
- [ ] `GET /team/feed {teamId}` → recent shared items + events.
- [ ] `GET /audit {project?, action?}` → audit entries; every delete and share is logged (FR-39).
- [ ] Resolve PRD OQ3: default to creator-initiated share (no approval) for v1; document.
- [ ] State tables for shares + audit (new migration).

## Files
- `go/internal/api/{team.go,audit.go}`
- `go/internal/state/{audit.go,migration_*.go}`

## Acceptance Criteria
- [ ] Scoped reads/writes isolate user/team/global correctly.
- [ ] Sharing a memory makes it visible to the team and appears in the feed.
- [ ] Every delete and share produces an audit entry (immutable/append-only).
- [ ] Private memories are not visible across users.

## Notes
Audit log is append-only — never updated or deleted (supports future compliance, PRD NFR-13). Keep scope-resolution in the state layer so all endpoints inherit it.
