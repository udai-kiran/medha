# Task 29: Observability — OTEL + metrics + logs

- **Milestone**: M5 — Viewer & Observability
- **Priority**: P0
- **Depends on**: Task 2, Task 3
- **Tech**: Go 1.26.3 / Python 3.14.5
- **Maps to**: PRD NFR-18, NFR-19, NFR-20, NFR-17; agent_mem.md §"Observability"

## Objective
Wire production observability across both services: OTEL traces, Prometheus metrics, structured logs, and a health endpoint reporting component status and latency percentiles.

## Scope & Steps
- [ ] Go `internal/telemetry/otel.go`: OTEL tracer/meter provider (OTLP exporter → Jaeger/Datadog); instrument API, search (per-modality spans), consolidation (per-step spans).
- [ ] Go `internal/telemetry/metrics.go`: Prometheus counters/histograms (`observations_total`, `memories_total`, `search_latency_ms`, `compression_duration_ms`, `llm_api_calls_total`, `llm_cost_usd`).
- [ ] Upgrade `logs.go` (Task 2) to emit the documented structured JSON fields (component, ids, duration_ms, provider, confidence).
- [ ] Enrich `GET /health` with component status (SQLite, Neo4j, queue, Python), error rates, and p50/p95/p99 latency.
- [ ] Python: `opentelemetry-instrumentation-fastapi` + real counters on `/metrics` (extraction/compression/embedding durations, LLM calls + cost).
- [ ] Propagate trace context across the Go↔Python HTTP boundary.

## Files
- `go/internal/telemetry/{otel.go,metrics.go,logs.go}`
- `go/internal/api/health.go` (enriched)
- `py/agent_mem/telemetry.py` (or extend `api.py`)

## Acceptance Criteria
- [ ] Traces for `smart-search` show per-modality child spans; consolidation shows per-step spans.
- [ ] Trace context propagates Go→Python (single distributed trace).
- [ ] `/metrics` (both services) expose the documented metrics; LLM cost tracked (NFR-17).
- [ ] `/health` reports per-component status + latency percentiles (NFR-19).

## Notes
Cost tracking (`llm_cost_usd`) feeds the PRD KPI on cost/session — ensure provider token usage is captured at the call site in the Python service.
