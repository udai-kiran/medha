# Task 12: Async queue integration

- **Milestone**: M2 — Compression & Search
- **Priority**: P0
- **Depends on**: Task 6, Task 11
- **Tech**: Go 1.26.3
- **Maps to**: PRD FR-10, FR-22, NFR-9; agent_mem.md §"Consolidation Orchestrator", §"Key Design Decisions #1"

## Objective
Add the async job substrate so capture stays non-blocking: a producer in the API and a consumer worker that drives compression (and later consolidation).

## Scope & Steps
- [ ] Define a `Queue` interface (`Publish(job)`, `Consume(handler)`) so the backend is swappable, implementing ADR-0001 (RabbitMQ in prod, in-memory in dev/test, Redis-capable later). OQ2 is already decided in Task 1 — do not re-open it here.
- [ ] Implement RabbitMQ backend (`internal/consolidation/queue.go`) using a maintained AMQP client; declare durable queues + dead-letter queue for failed jobs.
- [ ] Provide an in-memory backend for tests/lightweight mode.
- [ ] `cmd/worker/main.go`: standalone consumer process; config-driven; graceful shutdown.
- [ ] Job types: `compress{observationId, sessionId}` and `consolidate{sessionId, force}`.
- [ ] Worker calls Python `POST /compress`, then posts result to Go `POST /internal/observation/{id}/compressed` (Task 13 completes the loop).
- [ ] Add retry with backoff and DLQ routing on repeated failure.

## Files
- `go/internal/consolidation/{queue.go,queue_memory.go,queue_test.go}`
- `go/cmd/worker/main.go`

## Acceptance Criteria
- [ ] Publishing a `compress` job results in the worker invoking the Python service.
- [ ] Failed jobs land in the DLQ after configured retries.
- [ ] In-memory backend lets tests run without RabbitMQ.
- [ ] Worker shuts down gracefully, finishing in-flight jobs.

## Notes
Keep the `Queue` interface narrow; if RabbitMQ ops proves heavy, the in-memory/Redis backend can serve lightweight deployments (PRD NFR-24; see ADR-0001).
