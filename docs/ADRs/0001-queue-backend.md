# ADR-0001: Async job queue backend

- **Status**: Accepted
- **Date**: 2026-05-26
- **Resolves**: PRD `OQ2`
- **Related**: PRD FR-22, NFR-9, NFR-24; agent_mem.md §"Key Design Decisions #1"

## Context

agent_mem performs slow work (compression, consolidation, decay) asynchronously
so the capture hot path (`POST /observe`) can return in <50 ms. We need a
durable async job substrate that:

1. Survives Go-service restarts (jobs in flight must not be lost).
2. Supports retries + dead-letter queues for failed compressions.
3. Has a simple in-process backend for unit tests and lightweight deployments
   (NFR-24).
4. Leaves room for cloud-managed alternatives later (SQS, Redis Streams).

Candidates considered:

| Backend  | Pros | Cons |
|----------|------|------|
| RabbitMQ | Durable, DLQ, priority queues, mature AMQP ecosystem | Extra service to operate |
| Redis    | Simpler ops, multi-purpose (cache + queue) | DLQ semantics are weaker; durability config-dependent |
| NATS     | Lightweight, fast | Less mainstream tooling |
| SQS      | Fully managed | Cloud lock-in; latency |

## Decision

- Define a narrow `Queue` interface in Go (`Publish`, `Consume`) so the backend is swappable.
- **Production** backend = **RabbitMQ** (durable queues + DLQ on repeated failure).
- **Dev/test/lightweight** backend = **in-memory** channel (no external service required).
- Redis remains a future option behind the same interface; revisit if RabbitMQ ops prove heavy.

Selection is driven by env var `QUEUE_BACKEND=rabbitmq|memory`.

## Consequences

- Tests run with no external dependency.
- The lightweight deployment profile (no Neo4j, no RabbitMQ) is viable for hobbyist use.
- One extra abstraction layer to maintain; trivial since both backends implement the same 2 methods.
- Tasks 12 / 22 / 24 consume the same interface — adding a Redis backend later is a single new file, no caller changes.
