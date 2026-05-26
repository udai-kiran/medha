# Task 1: Repo & toolchain bootstrap

- **Milestone**: M0 — Scaffolding
- **Priority**: P0
- **Depends on**: none
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD NFR-21, NFR-22; agent_mem.md §"Directory Structure"

## Objective
Establish the monorepo layout, pin both toolchains, and add baseline tooling so all later tasks build on a consistent foundation.

## Scope & Steps
- [ ] Create top-level layout: `go/`, `py/`, `docs/`, `deploy/`, `.github/workflows/`.
- [ ] Initialize Go module in `go/`: `go mod init github.com/<org>/agent-mem` with `go 1.26.3` in `go.mod` (`toolchain go1.26.3`).
- [ ] Add `.tool-versions` (asdf) and/or `mise.toml` pinning `golang 1.26.3` and `python 3.14.5`.
- [ ] Initialize Python project in `py/` using `pyproject.toml` (PEP 621) with `requires-python = ">=3.14,<3.15"`; choose `uv` as the package manager and add `uv.lock`.
- [ ] Add root `Makefile` with targets: `setup`, `build`, `test`, `lint`, `run-go`, `run-py`, `compose-up`, `compose-down`.
- [ ] Add `.gitignore` (Go build artifacts, `__pycache__`, `.venv`, `*.db`, `.env`).
- [ ] Add `.env.example` capturing all variables from agent_mem.md §"Configuration" for both services.
- [ ] Add `golangci-lint` config (`.golangci.yml`) and Python `ruff` + `mypy` config in `pyproject.toml`.
- [ ] Add root `README.md` with quick-start pointing at Docker Compose (Task 4).
- [ ] Seed `docs/ADRs/` with the decisions resolved up front (so later tasks don't re-litigate them): ADR-0001 queue backend (RabbitMQ prod + in-memory dev, Redis-capable interface — resolves PRD OQ2), ADR-0002 decay constants are config-driven (resolves OQ1), ADR-0003 Neo4j optional / SQLite-only mode (NFR-24), ADR-0004 REST base path `/agentmemory`.

## Files
- `go/go.mod`, `go/go.sum`
- `py/pyproject.toml`, `py/uv.lock`
- `Makefile`, `.gitignore`, `.env.example`, `.tool-versions`
- `.golangci.yml`
- `docs/ADRs/{0001-queue-backend.md,0002-decay-constants.md,0003-neo4j-optional.md,0004-rest-base-path.md}`

## Acceptance Criteria
- [ ] `go version` resolves to 1.26.3 within `go/`; `go build ./...` succeeds on an empty skeleton.
- [ ] `python --version` resolves to 3.14.5 within `py/`; `uv sync` succeeds.
- [ ] `make lint` runs both `golangci-lint` and `ruff`/`mypy` with zero errors on the skeleton.
- [ ] `.env.example` lists every config var referenced by Tasks 2–3.
- [ ] ADR-0001..0004 exist, resolving PRD OQ2 and the decay/Neo4j-optional/endpoint-convention decisions before implementation starts.

## Notes
Keep Go and Python as independent build units (separate lockfiles, separate Dockerfiles) — they are deployed as distinct services.
