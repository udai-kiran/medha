# Task 5: CI pipelines (GitHub Actions)

- **Milestone**: M0 — Scaffolding
- **Priority**: P0
- **Depends on**: Task 2, Task 3
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD §10 (release plan), NFR-7; agent_mem.md §"Next Steps"

## Objective
Automated lint, build, and test on every PR for both services, plus image builds, so regressions are caught before merge.

## Scope & Steps
- [ ] `.github/workflows/go-test.yml`: setup-go `1.26.3`, cache modules, `go vet`, `golangci-lint`, `go test ./... -race -cover`.
- [ ] `.github/workflows/py-test.yml`: setup-python `3.14.5`, install `uv`, `ruff check`, `mypy`, `pytest --cov`.
- [ ] `.github/workflows/docker-build.yml`: build both images on push to main; push to registry on tagged release.
- [ ] `.github/workflows/integration-test.yml`: spin up compose stack, run cross-service smoke tests (placeholder until Task 34).
- [ ] Add coverage gate (warn < 70%) and PR status checks required for merge.
- [ ] Add a concurrency group to cancel superseded runs.

## Files
- `.github/workflows/{go-test.yml,py-test.yml,docker-build.yml,integration-test.yml}`

## Acceptance Criteria
- [ ] PRs trigger Go and Python jobs; both pass on the M0 skeleton.
- [ ] Pinned toolchains (`1.26.3`, `3.14.5`) are used in CI, matching local.
- [ ] Image build job produces both images successfully.
- [ ] Required status checks block merge on failure.

## Notes
Integration workflow will stay a smoke-test placeholder until Tasks 8/18/22 land real endpoints; wire the full matrix in Task 34.
