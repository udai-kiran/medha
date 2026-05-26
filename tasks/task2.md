# Task 2: Go service skeleton (Chi + config + health)

- **Milestone**: M0 — Scaffolding
- **Priority**: P0
- **Depends on**: Task 1
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-40, NFR-19, NFR-22; agent_mem.md §"Go Service"

## Objective
Stand up the Go HTTP service with the Chi router, structured config loading, graceful startup/shutdown, and a working health endpoint — the shell every later Go task plugs into.

## Scope & Steps
- [ ] Create `go/cmd/api/main.go`: load config, build router, listen on `:3111` (API) and `:3113` (viewer placeholder).
- [ ] Add `internal/config/config.go`: parse env vars (PORT, VIEWER_PORT, NEO4J_*, SQLITE_PATH, RABBITMQ_URL, PYTHON_SERVICE_URL, feature flags, AGENTMEMORY_SECRET, LOG_LEVEL) into a typed `Config` struct; `internal/config/validate.go` for required-field checks.
- [ ] Add `internal/api/middleware.go`: request ID, structured request logging, panic recovery, CORS.
- [ ] Add `internal/api/errors.go`: standard JSON error envelope `{error, message, fallback?}` + helpers.
- [ ] Add `GET /agentmemory/health` returning `{status, components, version}`.
- [ ] Implement graceful shutdown on SIGINT/SIGTERM (drain with context timeout).
- [ ] Wire Chi router groups: `/agentmemory/*` for public API, `/internal/*` for service-to-service callbacks.
- [ ] Add `internal/telemetry/logs.go` stub (structured JSON logger) used by middleware.
- [ ] Seed `docs/api/openapi.yaml` with the `/agentmemory` base path, the standard error envelope, and the `/agentmemory/health` route. Establish the convention that every route-adding task (8, 18, 25, 26, 33) updates this file; Task 35 only finalizes it.

## Files
- `go/cmd/api/main.go`
- `go/internal/config/{config.go,validate.go}`
- `go/internal/api/{router.go,middleware.go,errors.go,health.go}`
- `go/internal/telemetry/logs.go`
- `docs/api/openapi.yaml`

## Acceptance Criteria
- [ ] `go run ./cmd/api` starts and `curl :3111/agentmemory/health` returns `200` with JSON status.
- [ ] Invalid/missing required config exits non-zero with a clear message (validate.go).
- [ ] SIGTERM triggers graceful shutdown within the configured timeout.
- [ ] Panics in handlers are recovered and returned as `500` JSON, not crashes.
- [ ] `docs/api/openapi.yaml` validates and documents `/agentmemory/health` + the error envelope.

## Notes
Use stdlib `log/slog` for structured logging to avoid an extra dependency before Task 29 wires full OTEL.
