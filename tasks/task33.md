# Task 33: Auth, rate limiting, graceful shutdown, backup/restore

- **Milestone**: M6 — Production Hardening
- **Priority**: P0
- **Depends on**: Task 25
- **Tech**: Go 1.26.3
- **Maps to**: PRD NFR-11, NFR-6, NFR-13; agent_mem.md §"Phase 6: Production Hardening"

## Objective
Make the service production-safe: bearer-token auth, rate limiting, connection pooling, hardened shutdown, and backup/restore tooling.

## Scope & Steps
- [ ] Bearer-token auth middleware (`AGENTMEMORY_SECRET`) enforced on all mutating endpoints; configurable allow-list for read-only (NFR-11).
- [ ] Per-client/IP rate limiting (token bucket) with sane defaults + config.
- [ ] Connection pooling/tuning for SQLite, Neo4j, and the queue.
- [ ] Input sanitization pass across handlers; comprehensive error codes/messages.
- [ ] Graceful shutdown covering API, worker, viewer, and in-flight jobs.
- [ ] Backup: snapshot SQLite (online backup API) + optional Neo4j dump; `restore` command/endpoint.
- [ ] (P2) Right-to-be-forgotten: verifiable delete of a user/entity across SQLite + indexes + Neo4j (NFR-13).

## Files
- `go/internal/api/middleware.go` (auth + rate limit additions)
- `go/internal/state/backup.go`
- `go/cmd/api/main.go` (shutdown wiring)

## Acceptance Criteria
- [ ] Mutating endpoints reject missing/invalid tokens with `401`.
- [ ] Rate limiter returns `429` past the threshold.
- [ ] Backup produces a restorable snapshot; restore round-trips data.
- [ ] Graceful shutdown drains in-flight work within the timeout.

## Notes
Don't skip auth on `/internal/*` service-to-service routes — restrict them to the internal network or a separate token.
