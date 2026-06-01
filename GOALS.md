# medha — Implementation Goals

Feature roadmap derived from `reference/LOW_LEVEL_DESIGN.md` (agentmemory Node.js) and
`reference/DESIGN.md` (Neo4j Agent Memory). Each goal is a discrete, shippable unit.

**Current baseline:** core pipeline (observe → compress → index → consolidate → decay),
BM25 + vector + graph hybrid search (RRF), 6 MCP tools, PostgreSQL, Bifrost-only LLM.

---

## Phase 1 — Core Completeness

### G01 · LLM title generation + dedup search in `remember`
When `title` is omitted, call Python `/compress` (or a new `/title` endpoint) to generate
a semantic title via LLM. Before inserting, run a similarity search against existing
memories and return any close matches (score ≥ 0.85) so the caller can decide to update
instead of duplicate. Add `PythonServiceURL` to `MemoryToolsDeps`.

### G02 · File history tool
Add `file-history` MCP tool and `GET /agentmemory/file-history?project=&filePath=`.
Queries observations where `files[]` contains the given path; returns chronological list
of `{obsId, action, title, timestamp}`. Backed by a PostgreSQL index on the files JSONB
column.

### G03 · Project profile tool
Add `profile` MCP tool and `GET /agentmemory/profile?project=`. Aggregates top concepts
(by frequency across compressed observations), top files (by observation count), and top
recurring patterns. Computed via SQL aggregation; used for context injection.

### G04 · Pattern detection
Add `patterns` MCP tool and REST endpoint. Detects recurring patterns across compressed
observations using concept/file co-occurrence scoring. Returns `[{pattern, count,
examples[]}]`. Stores detected patterns in a `patterns` table so they survive session
boundaries.

### G05 · Timeline view
Add `timeline` MCP tool and `GET /agentmemory/timeline`. Chronological observations for a
project or session with sliding-window pagination. Supports filtering by hookType, date
range, and file path.

### G06 · Export / Import + Snapshots
- `GET /agentmemory/export` — full JSON dump (sessions, observations, memories, graph)
- `POST /agentmemory/import` — restore from JSON dump
- `POST /agentmemory/snapshot/create` + `GET /agentmemory/snapshot/:id`
- `POST /agentmemory/import-jsonl` — ingest Claude Code `.jsonl` transcript files and
  replay as observations

### G07 · Diagnose / Heal endpoints
- `GET /agentmemory/diagnose` — checks DB connectivity, index consistency (BM25/vector
  doc count vs observations table), Python service reachability, Neo4j status, queue
  depth, decay job last run
- `POST /agentmemory/heal` — auto-fixes stuck state: reindex orphaned observations, reset
  stuck sessions, flush dead compression jobs

---

## Phase 2 — Extended Memory Types

### G08 · Short-term conversation memory
Add `conversations` and `messages` tables (mirroring DESIGN.md schema). Implement
add_message, get_conversation, search_messages, list_sessions, clear_session,
get_conversation_summary. Messages link sequentially (NEXT_MESSAGE chain). Entity
mentions linked to graph entities. MCP tools: `memory_store_message`,
`memory_get_conversation`, `memory_list_sessions`.

### G09 · Preferences memory
Add `preferences` table (`id, project, category, preference, confidence, metadata,
created_at, updated_at, superseded_by`). Implement add_preference, search_preferences,
get_preferences, delete_preference. Add pattern-based preference detector that auto-fires
on message storage ("I love X", "I prefer Y"). MCP tool: `memory_add_preference`.

### G10 · Facts memory (subject–predicate–object)
Add `facts` table (`id, project, subject, predicate, object_value, confidence,
created_at`). Implement add_fact, search_facts (full-text + filter by subject/predicate).
MCP tool: `memory_add_fact`. Facts are a lightweight triple store alongside graph
entities.

### G11 · Reasoning trace memory
Add `reasoning_traces`, `reasoning_steps`, `tool_calls` tables (mirroring DESIGN.md).
Implement start_trace, record_step, record_tool_call, complete_trace, get_trace,
search_traces. Add streaming trace recorder in Go. Links traces to messages and touched
entities for audit. MCP tools: `memory_start_trace`, `memory_record_step`,
`memory_complete_trace`, `memory_get_observations`.

---

## Phase 3 — Entity Intelligence

### G12 · Entity deduplication (SAME_AS)
Add `entity_same_as` table (`source_id, target_id, confidence, match_type,
status: pending/confirmed/rejected`). On UpsertEntity, run composite resolver: exact →
fuzzy Levenshtein → semantic embedding similarity. Auto-merge ≥ 0.95, flag for review
0.85–0.95. Add review_duplicate, find_potential_duplicates, get_same_as_cluster APIs.

### G13 · Entity enrichment (Wikipedia + Diffbot)
Background enrichment after entity creation. Wikipedia: store enriched_description,
wikipedia_url, wikidata_id, image_url. Diffbot (optional, `DIFFBOT_API_KEY`): richer
org/person data. Rate-limited (WIKIPEDIA_RATE_LIMIT already in config). SQLite cache to
avoid repeated lookups. Only enriches entities above min_confidence threshold.

### G14 · Geocoding for LOCATION entities
Add lat/lon to graph_entities. Nominatim provider (free, rate-limited). Auto-geocode on
entity creation when `type=LOCATION` and `geocode=true`. Add
`search_locations_near(lat, lon, radius_km)` and `search_locations_in_bounding_box`
endpoints. `GEOCODING_PROVIDER` env var for optional Google override.

### G15 · Multi-tenant support (user_identifier)
Add optional `user_id` column to sessions, observations, memories, conversations,
preferences, facts, reasoning_traces. Add `users` table. All queries accept optional
`user_identifier` filter — scopes results to that user. Bearer token can encode user
identity.

---

## Phase 4 — Orchestration

### G16 · Actions + Frontier (DAG work items)
Add `actions` table (`id, project, title, status, dependencies[], priority, due_at,
result`). Implement create_action, update_action, get_frontier (unblocked actions ranked
by priority), get_next (single highest-priority unblocked action). MCP tools:
`memory_action_create`, `memory_action_update`, `memory_frontier`, `memory_next`.

### G17 · Leases (multi-agent exclusive locks)
Add `leases` table (`id, action_id, agent_id, expires_at`). Implement acquire_lease (fail
if unexpired lease exists), renew_lease, release_lease. Background janitor expires stale
leases. MCP tool: `memory_lease`. REST: `POST /agentmemory/leases/:actionId`.

### G18 · Routines (workflow templates)
Add `routines` table (`id, project, name, template_actions[], params_schema,
executions[]`). Implement create_routine, run_routine (instantiates ordered actions from
template with param substitution), list_routines. MCP tool: `memory_routine_run`.

### G19 · Signals (inter-agent messaging)
Add `signals` table (`id, from_agent, to_agent, message, metadata, sent_at, read_at`).
Implement send_signal, read_inbox (with receipt tracking), mark_read. MCP tools:
`memory_signal_send`, `memory_signal_read`. REST: `POST /agentmemory/signals`,
`GET /agentmemory/signals/inbox`.

### G20 · Checkpoints + Sentinels
- **Checkpoints:** `checkpoints` table (`id, project, condition_expr, satisfied_at`).
  `POST /agentmemory/checkpoints/:id/check` evaluates condition and marks satisfied.
- **Sentinels:** `sentinels` table (`id, project, event_pattern, handler_url,
  triggered_at`). Fires HTTP callback when matching event occurs (hook type, memory tier
  change). MCP tools: `memory_checkpoint`, `memory_sentinel_create`,
  `memory_sentinel_trigger`.

### G21 · Sketches + Crystallization
- **Sketches:** ephemeral in-memory action graphs. `sketch_create` builds transient DAG,
  `sketch_promote` converts to permanent routine.
- **Crystallization:** `crystallize` compacts a linear chain of completed actions into a
  single summary action. MCP tools: `memory_sketch_create`, `memory_sketch_promote`,
  `memory_crystallize`.

---

## Phase 5 — Collaboration & Governance

### G22 · Team sharing + feed
Add `team_shared` table (`memory_id, team_id, shared_by, mode: read|edit, shared_at`) and
`team_feed` table. Implement share_memories, get_team_feed (last 24h). `TEAM_ID` env var
sets default team scope. MCP tools: `memory_team_share`, `memory_team_feed`.

### G23 · Governance (extended audit + compliance delete)
Extend audit_log to cover all mutations (memory creates/updates/deletes, session state
changes, compression jobs). Add `POST /agentmemory/governance/delete` (hard-delete with
mandatory reason + audit record, for GDPR/CCPA). Add `GET /agentmemory/audit` with
filters. MCP tools: `memory_audit`, `memory_governance_delete`.

### G24 · Slots (pinned editable memory slots)
Add `slots` table (`id, project, slot_name, content, updated_at`). Named slots are
always-injected context blocks: persona, preferences, guidance, pending_items. `slot_set`
upserts, `slot_get` retrieves. Feature flag: `AGENTMEMORY_SLOTS=true` (already in .env).
On SessionEnd, `slot_reflect` appends unresolved TODOs to `pending_items` slot.

### G25 · Working memory stack
Session-scoped LIFO stack for ephemeral context. Add `working_memory` table (`id,
session_id, context, pushed_at`). Implement working_push, working_pop (N items),
working_clear. Auto-cleared on SessionEnd. MCP tools: `memory_working_push`,
`memory_working_pop`, `memory_working_clear`.

---

## Phase 6 — Advanced Retrieval

### G26 · Facets (dimension:value tagging + filtering)
Add `memory_facets` table (`memory_id, dimension, value`). Implement facet_tag (add
dimension:value to a memory), facet_query (filter memories by one or more
dimension:value pairs). Example: `{layer: "frontend", status: "confirmed"}`. MCP tools:
`memory_facet_tag`, `memory_facet_query`.

### G27 · Lessons extraction + query
Add `lessons` table (`id, project, session_id, lesson, context, strength, timestamp`).
`lessons_extract` runs on SessionEnd: sends observations to Python and prompts for
lessons-learned. `lessons_query` retrieves by topic via hybrid search. Strength decays
like memories. MCP tools: `memory_lessons_extract`, `memory_lessons_query`. REST:
`GET /agentmemory/lessons`.

### G28 · Skills extraction + search
Add `skills` table (`id, project, skill_name, level: novice/competent/expert,
evidence_count, last_demonstrated_at`). `skill_extract` detects acquired skills from
session observations. `skill_search` finds memories demonstrating a named skill. Level
upgrades as evidence accumulates. MCP tools: `memory_skill_extract`,
`memory_skill_search`.

---

## Phase 7 — Platform

### G29 · Multimodal / Vision (image embedding + search)
Image storage (file path reference in observations). Python sidecar: add `/embed-image`
using a vision model via Bifrost (LLM vision description → text embedding). Go:
`POST /agentmemory/vision/embed`, `POST /agentmemory/vision/search`. Store image
embeddings in vector_docs alongside text. MCP tools: `vision_embed_image`,
`vision_search_images`.

### G30 · Mesh (P2P sync between instances)
Add `POST /agentmemory/mesh/sync` with `{peer: {host, port}, mode: push|pull}`. Push:
serialize memories/observations since last sync and POST to peer's `/import`. Pull: GET
peer's export and merge locally. Conflict resolution: last-write-wins on content, max on
strength. Add `last_sync_at` tracking per peer.

### G31 · Consolidation maintenance jobs
Scheduled jobs alongside existing nightly decay:
1. `dedupe_entities` — auto-merge SAME_AS clusters above threshold
2. `summarize_long_traces` — LLM-compress reasoning traces with 50+ steps
3. `detect_superseded_preferences` — mark old preferences superseded by newer ones
4. `archive_expired_conversations` — TTL-based conversation archival

All jobs configurable and triggerable manually via REST.

---

## Phase 8 — MCP Surface

### G32 · Expand MCP to full tool surface (53 tools)
Wire all features from G01–G31 into MCP tools. Target: 8 core tools always available +
45 extended tools behind `AGENTMEMORY_TOOLS=all`. Add 6 MCP resources (status, project
profile, latest memories, trending memories, graph stats, active sessions). Add 3 MCP
prompts (recall_context, session_handoff, detect_patterns).

### G33 · MCP shim / fallback package
Lightweight shim that proxies MCP calls to the running server at `AGENTMEMORY_URL`. When
server is unreachable, falls back to a bundled 7-core-tool implementation (smart-search,
recall, remember, forget, session-history, status, profile) that talks directly to
PostgreSQL. Agents degrade gracefully if the full server crashes.

### G34 · Context assembly (get_context across all memory types)
Add `POST /agentmemory/context`. Assembles formatted context string for agent prompt
injection from: short-term messages, long-term memories (semantic search hits),
preferences, facts, active slots, pending_items, reasoning traces (similar past tasks).
Configurable via `include_short_term`, `include_long_term`, `include_reasoning`,
`max_items`. Returns context string + token count + source list.
