# PRD: agent_mem — Hybrid Agent Memory System

**Version**: 0.1
**Date**: 2026-05-26
**Status**: Draft for review
**Owner**: udai.kiran@oneconvergence.com

**Supporting documents:**
- [`agent_mem.md`](./agent_mem.md) — Technical architecture & implementation plan (Go + Python)
- [`FEATURE_ANALYSIS.md`](./FEATURE_ANALYSIS.md) — Comparative analysis of the two source systems
- [`reference/DESIGN.md`](./reference/DESIGN.md) — Neo4j Agent Memory source design (structured entity graph)
- [`reference/LOW_LEVEL_DESIGN.md`](./reference/LOW_LEVEL_DESIGN.md) — agentmemory source design (hook-based capture + consolidation)

> **Canonical sources:** This `PRD.md` and `agent_mem.md` are the canonical definition of the target system (Go + Python; SQLite + Neo4j + queue). `DESIGN.md` and `LOW_LEVEL_DESIGN.md` are **reference/source designs only** — they describe the two prior systems being combined (the latter is a Node.js/iii, KV-only design with no external DB) and do not dictate the target stack. Where they conflict, this PRD and `agent_mem.md` win.

---

## 1. Overview

agent_mem is a persistent, long-term memory service for AI coding agents (Claude Code, Cursor, Cline, OpenCode, and similar). It automatically captures what an agent does, distills those observations into searchable knowledge, and recalls the right context on demand — so agents stop re-learning the same project facts every session.

The product unifies two complementary approaches: **automatic hook-based observation capture with human-like consolidation/decay** (from agentmemory) and **structured entity knowledge graphs with rich extraction and enrichment** (from Neo4j Agent Memory). The result is a system that captures everything with zero integration burden, then organizes it into a typed, decaying knowledge graph that supports hybrid search.

This PRD defines *what* the product must do and *why*. The *how* lives in [`agent_mem.md`](./agent_mem.md).

---

## 2. Problem Statement

AI coding agents are stateless across sessions. Each new conversation starts cold:

1. **Lost context.** Decisions, constraints, and rationale from prior sessions evaporate. Agents re-discover the same architecture, re-read the same files, and re-ask answered questions.
2. **High integration cost.** Existing memory tools require agents to make explicit, manual API calls to store and retrieve — a burden most agents never adopt consistently.
3. **No forgetting.** Naive memory stores accumulate stale, contradictory, or low-value facts that pollute retrieval and grow unboundedly.
4. **Weak retrieval.** Pure keyword or pure vector search misses relationship context ("what depends on this function?") that matters for code.
5. **No structure.** Flat note stores can't answer entity-level questions ("who owns this service?", "where is X deployed?") or traverse relationships.
6. **Single-agent assumption.** Teams of agents (or humans + agents) have no shared, governed memory with provenance and audit.

**Consequence:** wasted tokens, repeated work, inconsistent decisions, and agents that never get smarter about a specific codebase or organization.

---

## 3. Goals & Non-Goals

### 3.1 Goals

- **G1 — Zero-integration capture.** Memory is populated automatically from agent lifecycle hooks; agents need not make manual store calls.
- **G2 — High-quality recall.** Hybrid search (keyword + semantic + graph) returns the right context with R@10 ≥ 95% on a representative benchmark.
- **G3 — Human-like memory lifecycle.** Memories consolidate across tiers and decay realistically, so retrieval stays high-signal over time.
- **G4 — Structured knowledge.** Observations resolve into typed entities (POLE+O) with relationships, enrichment, and provenance.
- **G5 — Privacy by default.** Secrets and marked-private content are filtered at capture, before any storage or LLM call.
- **G6 — Production-grade operability.** First-class observability, health, backup/restore, and predictable performance.
- **G7 — Team-ready.** Namespacing, shared memory, inter-agent signaling, and a governance audit trail.

### 3.2 Non-Goals (v1)

- **NG1** — Not a general-purpose vector database or a replacement for application data stores.
- **NG2** — Not a model-training pipeline; agent_mem stores and retrieves, it does not fine-tune models.
- **NG3** — Not an agent framework. It provides orchestration *primitives* (Actions, Leases, Signals) but does not dictate agent control flow.
- **NG4** — No learning-to-rank/ML reranker in v1 (RRF fusion only; ML reranker is a future extension).
- **NG5** — No P2P mesh replication in v1 (single-instance deployment; replication is future work).
- **NG6** — Not responsible for agent prompt construction; it returns context, the agent decides how to inject it.

---

## 4. Target Users & Personas

| Persona | Description | Primary needs |
|---------|-------------|---------------|
| **Agent Author** | Developer integrating agent_mem into an agent (e.g., wiring Claude Code hooks). | Trivial setup, MCP tools, sane defaults, no LLM key required to start. |
| **End Developer** | Engineer whose coding sessions are captured and recalled. | Accurate recall, no leaked secrets, low latency, no workflow disruption. |
| **Team Lead** | Owns a team of humans + agents sharing project memory. | Shared memory, provenance, audit trail, access control. |
| **Platform/SRE** | Operates the service in production. | Observability, health checks, backup/restore, predictable cost & resource use. |

---

## 5. Use Cases

- **UC1 — Cross-session recall.** An agent resumes work and asks "what did we decide about auth?" agent_mem returns the prior decision, rationale, and touched files.
- **UC2 — Automatic capture.** As an agent reads files and runs tools, observations are captured, compressed, and indexed without explicit calls.
- **UC3 — Entity lookup.** "What does `validateToken` depend on?" returns graph neighbors (jose library, auth.ts) with confidence.
- **UC4 — Session consolidation.** At session end, observations are summarized into a session summary plus reusable memories (architecture decisions, patterns, bugs).
- **UC5 — Decay & cleanup.** Stale, unreinforced memories fade automatically; high-value memories reinforced on retrieval persist.
- **UC6 — Secret protection.** A tool output containing an API key is redacted before storage; downstream LLM extraction never sees it.
- **UC7 — Team sharing.** An agent marks a memory "team", another agent on the team recalls it; the share is recorded in the audit trail.
- **UC8 — Live observability.** An operator watches observations, memories, and search activity stream in real time and inspects health/latency.
- **UC9 — Multi-agent coordination.** Agents claim work via Leases and message each other via Signals to avoid duplicate effort.

---

## 6. Functional Requirements

Priorities: **P0** = v1 must-have, **P1** = v1 should-have, **P2** = later/nice-to-have.

> **Route convention:** all REST routes are served under the `/agentmemory` base path; the short names below (e.g. `/observe`, `/smart-search`) omit the prefix for brevity.

### 6.1 Observation Capture
- **FR-1 (P0)** Accept observations via `POST /observe` from agent lifecycle hooks (SessionStart, PostToolUse, PostToolFailure, UserPrompt, SessionEnd, etc.).
- **FR-2 (P0)** Validate required fields and reject malformed payloads with actionable errors.
- **FR-3 (P0)** Deduplicate within a 5-minute SHA-256 rolling window per session; return `202` for duplicates.
- **FR-4 (P0)** Persist a `RawObservation` and return `201` with `{observationId, compressing}` without blocking on compression.
- **FR-5 (P1)** Extract and store image/multimodal payloads (`data:image/...`) for later vision description.

### 6.2 Privacy Filtering
- **FR-6 (P0)** Strip secret patterns (API keys, `password=`, `token=`, `secret=`) before any persistence.
- **FR-7 (P0)** Remove `<private>…</private>` blocks entirely before storage.
- **FR-8 (P0)** Strip ANSI escape codes from captured output.
- **FR-9 (P1)** Flag entities containing sensitive patterns as "sensitive" and skip external enrichment for them.

### 6.3 Compression & Extraction
- **FR-10 (P0)** Compress observations asynchronously into `CompressedObservation` (type, title, facts, narrative, concepts, files, importance, confidence).
- **FR-11 (P0)** Provide a **synthetic** (zero-LLM) compression path that always works without an API key.
- **FR-12 (P1)** Provide an **LLM** compression path (configurable provider) with timeout and automatic fallback to synthetic.
- **FR-13 (P1)** Extract typed entities (POLE+O) via spaCy → GLiNER → LLM fallback.
- **FR-14 (P1)** Extract typed relationships (DEPENDS_ON, IMPLEMENTS, WORKS_AT, RELATED_TO, CONTRADICTS, SUPERSEDES, DERIVED_FROM) with confidence + source provenance.
- **FR-15 (P2)** Describe images via LLM vision for multimodal observations.

### 6.4 Search & Retrieval
- **FR-16 (P0)** Provide hybrid `POST /smart-search` combining BM25 + vector + graph via RRF fusion (k=60).
- **FR-17 (P0)** Support single-mode search (`bm25`, `vector`, `graph`) for debugging and fallback.
- **FR-18 (P0)** Apply diversity boost (cap results per session) and return relevance-scored results with snippets.
- **FR-19 (P1)** Provide `GET /context` returning formatted, injection-ready context for a session.
- **FR-20 (P1)** Provide entity/graph retrieval (`/get_entity`, `/get_graph`, `/relations`) with configurable traversal depth and confidence filtering.
- **FR-21 (P1)** Provide `/file_history` and `/timeline` chronological views.

### 6.5 Consolidation
- **FR-22 (P0)** Trigger consolidation on SessionEnd via an async job; respond `202` immediately.
- **FR-23 (P0)** Produce a `SessionSummary` (title, narrative, key decisions, files modified, concepts).
- **FR-24 (P1)** Cluster observations and extract reusable `Memory` objects (architecture, pattern, bug, workflow, preference, fact).
- **FR-25 (P1)** Build/merge the entity graph during consolidation (dedup + relationship edges + enrichment).
- **FR-26 (P0)** Organize memory into 4 tiers: Working (24h), Episodic (7d), Semantic (decay), Procedural (decay).

### 6.6 Decay & Retention
- **FR-27 (P0)** Hard-evict Working-tier observations after 24h and archive Episodic observations after 7d.
- **FR-28 (P1)** Apply Ebbinghaus decay (`strength *= 0.95^daysOld`) to Semantic/Procedural memories on a nightly job.
- **FR-29 (P1)** Reinforce strength on retrieval (capped) so frequently-used memories persist.
- **FR-30 (P1)** Hard-evict memories below strength 0.1; surface 0.1–0.3 band for optional review.

### 6.7 Entity Enrichment
- **FR-31 (P2)** Enrich entities from Wikipedia/Wikidata/Diffbot (background, rate-limited) with description, URL, IDs, image.
- **FR-32 (P2)** Cache enrichment results locally to respect external API limits.

### 6.8 Orchestration (Multi-Agent)
- **FR-33 (P1)** Provide Action DAG primitives (`/actions`, `/frontier`, `/next`) with dependencies.
- **FR-34 (P1)** Provide Leases for exclusive multi-agent action ownership.
- **FR-35 (P2)** Provide Signals (inter-agent messaging with receipts) and Routines (reusable workflow templates).
- **FR-36 (P2)** Link ReasoningTraces to Actions and support crystallization of step chains into reusable procedures.

### 6.9 Team & Governance
- **FR-37 (P1)** Namespace memory by global / team / user scopes.
- **FR-38 (P1)** Support sharing memories to a team with read/edit modes and a team feed.
- **FR-39 (P0)** Maintain a governance audit trail logging every deletion and share.

### 6.10 Tool & API Surface
- **FR-40 (P0)** Expose capabilities via REST API.
- **FR-41 (P0)** Expose an MCP server (stdio + HTTP proxy) with tools, resources, prompts, and slash skills (`/recall`, `/remember`, `/session-history`, `/forget`).

### 6.11 Real-Time Viewer
- **FR-42 (P1)** Stream observations, memories, and graph updates over WebSocket.
- **FR-43 (P1)** Provide a dashboard for sessions, memories, search results, and a knowledge-graph view.

---

## 7. Non-Functional Requirements

### 7.1 Performance
- **NFR-1 (P0)** Search latency at 10K observations: p50 < 50 ms, p95 < 150 ms, p99 < 500 ms.
- **NFR-2 (P0)** `POST /observe` returns in < 50 ms (capture is non-blocking; compression is async).
- **NFR-3 (P1)** Synthetic compression < 100 ms; LLM compression avg 2–3 s with enforced timeout + fallback.
- **NFR-4 (P1)** Consolidation completes in 5–30 s per session depending on observation count.
- **NFR-5 (P1)** Nightly decay job completes in < 1 min for 100K memories.

### 7.2 Reliability
- **NFR-6 (P0)** Service uptime target 99.5% (excluding planned maintenance).
- **NFR-7 (P0)** Non-transient error rate < 0.1%.
- **NFR-8 (P0)** Deduplication accuracy ≥ 99% within the dedup window.
- **NFR-9 (P0)** Degrade gracefully: if Python/LLM/Neo4j are unavailable, capture and synthetic compression still succeed.

### 7.3 Security & Privacy
- **NFR-10 (P0)** Secret filtering runs before persistence and before any external/LLM call (defense in depth).
- **NFR-11 (P0)** Bearer-token auth on all mutating endpoints; configurable secret.
- **NFR-12 (P1)** Observations are private to their creator by default; sharing is explicit.
- **NFR-13 (P2)** Support GDPR-style right-to-be-forgotten (safe, verifiable deletion).

### 7.4 Scalability & Cost
- **NFR-14 (P1)** Handle 1K observations/session and 100 concurrent agent sessions in load tests.
- **NFR-15 (P1)** Resource budget: Go service < 100 MB RSS, Python service < 500 MB RSS under nominal load.
- **NFR-16 (P1)** Storage budget: ~2 KB per compressed observation, ~5 KB per memory.
- **NFR-17 (P1)** LLM/embedding cost tracked and exportable per project.

### 7.5 Observability
- **NFR-18 (P0)** Structured JSON logs with component, session/observation IDs, durations.
- **NFR-19 (P0)** Health endpoint reporting component status, error rates, latency percentiles.
- **NFR-20 (P1)** OTEL traces for API, search, and consolidation spans; Prometheus metrics (counts, latency, LLM calls/cost).

### 7.6 Portability & Deployment
- **NFR-21 (P0)** One-command local bring-up via Docker Compose (Go + Python + Neo4j + queue).
- **NFR-22 (P1)** Single-binary Go service; Python service as a slim container.
- **NFR-23 (P2)** Kubernetes manifests for production deployment.
- **NFR-24 (P0)** Work without Neo4j in a degraded mode (SQLite-only graph) for lightweight deployments.

---

## 8. System Architecture (Summary)

Full detail in [`agent_mem.md`](./agent_mem.md). At a glance:

- **Go service (`:3111`, viewer `:3113`)** — capture, validation, dedup, privacy filtering, state (SQLite), hybrid search, RRF fusion, consolidation orchestration, REST API, MCP server, WebSocket viewer, Neo4j graph ops, telemetry.
- **Python service (`:5000`)** — entity extraction (spaCy/GLiNER/LLM), relationship extraction, compression, summarization, embeddings, enrichment.
- **Storage** — SQLite (observations, memories, KV state, BM25/vector indexes) + Neo4j (entity graph, relationships, enrichment). Neo4j optional for lightweight mode.
- **Async** — queue-backed jobs for compression and consolidation; nightly decay job.
- **Inter-service** — HTTP REST (sync extraction) + queue (async jobs); ~5% latency overhead budget.

**Rationale & trade-offs** (RabbitMQ vs. alternatives, SQLite+Neo4j duality, RRF vs. ML reranker, 4-tier memory, Ebbinghaus decay, Python extraction service) are documented in `agent_mem.md` §"Key Design Decisions".

---

## 9. Success Metrics (KPIs)

| Metric | Target | Why it matters |
|--------|--------|----------------|
| Search recall @10 | ≥ 95% on benchmark (e.g., LongMemEval-S) | Recall quality (G2) |
| Search latency p95 | < 150 ms @ 10K obs | Responsiveness (NFR-1) |
| Capture success rate | ≥ 99.9% | Zero-integration reliability (G1) |
| Dedup accuracy | ≥ 99% | Storage hygiene (NFR-8) |
| Secret leak incidents | 0 | Privacy by default (G5) |
| Adoption: % sessions auto-captured | ≥ 90% of integrated agents | Integration burden (G1) |
| Recall usefulness | qualitative: agents reuse recalled context | Core value (UC1) |
| LLM cost / session | ≤ ~$0.01 (compression+summary+extraction) | Cost predictability (NFR-17) |

---

## 10. Release Plan

Mapped from the implementation roadmap in `agent_mem.md`. Timeline is indicative.

| Milestone | Scope | Exit criteria |
|-----------|-------|---------------|
| **M0 — Scaffolding** | Go + Python skeletons, Docker Compose, CI | Both services start; health checks pass |
| **M1 — Capture pipeline** | FR-1..9, synthetic compression | Hook → RawObservation stored; secrets filtered |
| **M2 — Compression & search** | FR-10..18, BM25+vector+graph RRF | End-to-end: observe → compress → search works |
| **M3 — Consolidation & decay** | FR-22..30, FR-13..14 | SessionEnd → summary + memories; nightly decay runs |
| **M4 — REST + MCP** | FR-40..41, full route set | Agents use all tools via MCP and REST |
| **M5 — Viewer & observability** | FR-42..43, NFR-18..20 | Live streams + dashboard + traces/metrics |
| **M6 — Production hardening** | NFR-6..16, FR-31..39 | Load/chaos tests pass; backup/restore; docs |

**v1 (GA) = M0–M4 + privacy/observability baseline.** Orchestration depth (FR-35/36), enrichment (FR-31/32), and K8s (NFR-23) may land in M5/M6 or post-GA.

---

## 11. Dependencies

- **Neo4j 5.x** (optional; degraded SQLite-only mode supported per NFR-24).
- **Message queue** (RabbitMQ or Redis) for async jobs.
- **LLM provider** (Anthropic/OpenAI/Gemini) — optional; synthetic path runs without it.
- **Embedding provider** (local Xenova / OpenAI / Gemini / Voyage) — local default requires no key.
- **NLP models** (spaCy, GLiNER) bundled in the Python image.

---

## 12. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| LLM latency/cost unpredictable | Slow/expensive compression | Synthetic fallback (FR-11), timeouts (FR-12), cost metrics (NFR-17) |
| Dual store (SQLite+Neo4j) consistency lag | Stale graph reads | SQLite is hot path; Neo4j async enrichment; optional Neo4j (NFR-24) |
| Hook integration not adopted by agents | No capture, no value | MCP-first + sane defaults; document per-agent hook wiring |
| RRF heuristic misses edge cases | Lower recall on some queries | Monitor recall metric; tune k; ML reranker as future extension |
| Privacy filter misses a secret pattern | Secret leak | Layered filtering (FR-6..9), deny external enrichment for sensitive entities, test corpus of secret patterns |
| Decay evicts a still-valuable memory | Lost knowledge | Reinforcement on retrieval (FR-29), review band 0.1–0.3 (FR-30), manual "always keep" |
| Queue/Python outage | Pipeline stall | Graceful degradation (NFR-9); durable queue; capture stays up |

---

## 13. Assumptions & Open Questions

**Assumptions**
- Agents can be configured to emit lifecycle hooks (or use the MCP integration).
- Single-instance deployment is acceptable for v1 (no multi-region/P2P).
- Embedding dimensions are consistent within a project namespace.

**Open questions**
- **OQ1** — Default decay constant (0.95/day) and eviction threshold (0.1): tune against real usage before GA?
- **OQ2** — Queue choice for v1: RabbitMQ (durable, DLQ) vs. Redis (simpler ops)? `agent_mem.md` leans RabbitMQ.
- **OQ3** — Should team sharing require explicit approval, or is creator-initiated share sufficient for v1?
- **OQ4** — Benchmark dataset for the R@10 target — adopt LongMemEval-S, or build a project-specific eval set?
- **OQ5** — Multi-tenancy isolation level: namespace-only vs. per-tenant DB?

---

## 14. References

- [`agent_mem.md`](./agent_mem.md) — implementation plan, data flows, API contracts, deployment.
- [`FEATURE_ANALYSIS.md`](./FEATURE_ANALYSIS.md) — 10 combinable mechanisms, comparative matrix, trade-offs.
- [`reference/DESIGN.md`](./reference/DESIGN.md) — Neo4j Agent Memory (structured entity graph, POLE+O, enrichment).
- [`reference/LOW_LEVEL_DESIGN.md`](./reference/LOW_LEVEL_DESIGN.md) — agentmemory (hook capture, 4-tier consolidation, hybrid search, orchestration).
