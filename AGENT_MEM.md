# agent-mem MCP Tool Reference

The `agent-mem` MCP server is a Streamable HTTP server (port 3114) providing persistent memory, multi-agent orchestration, tracing, and knowledge management. All tools are prefixed `mcp__agent-mem__` in Claude Code.

Most tools accept an optional `project` parameter to namespace data. Omit it to use the default namespace.

---

## Core Memory

### `remember`
Persist a new memory. The only required field is `content`; everything else is inferred or optional.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `content` | string | yes | The memory text |
| `title` | string | no | Short label; auto-generated when omitted |
| `type` | string | no | `fact`, `preference`, `project`, `feedback` — defaults to `fact` |
| `tier` | enum | no | `working`, `episodic`, `semantic`, `procedural` |
| `concepts` | string[] | no | Tags/concepts to associate |
| `files` | string[] | no | File paths this memory relates to |
| `project` | string | no | Project namespace |

```
remember(content="User prefers terse responses with no trailing summaries", type="preference")
```

---

### `recall`
Retrieve a single memory by ID, reinforcing its Ebbinghaus decay strength (retrieved memories decay more slowly).

| Parameter | Type | Required |
|-----------|------|----------|
| `memoryId` | string | yes |

```
recall(memoryId="mem_abc123")
```

---

### `forget`
Hard-delete a memory by ID. Writes an audit log entry.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `memoryId` | string | yes | |
| `reason` | string | no | Explanation for deletion |
| `actor` | string | no | Who is deleting |

```
forget(memoryId="mem_abc123", reason="outdated preference")
```

---

### `smart-search`
Hybrid search over compressed observations using BM25 (keyword) + vector (cosine similarity) + graph (entity BFS), fused via Reciprocal Rank Fusion. The most powerful retrieval tool.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `query` | string | yes | Natural language search query |
| `mode` | enum | no | `bm25`, `vector`, `graph`, `hybrid` (default: `hybrid`) |
| `limit` | integer | no | 1–50 results |
| `project` | string | no | |

```
smart-search(query="user preferences for code style", mode="hybrid", limit=10)
```

---

## Structured Knowledge

### `add-fact`
Store a subject–predicate–object triple for structured, graph-queryable knowledge.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `subject` | string | yes | e.g. `"user"` |
| `predicate` | string | yes | e.g. `"prefers"` |
| `object` | string | yes | e.g. `"Go over Python"` |
| `confidence` | number | no | 0.0–1.0 |
| `project` | string | no | |

```
add-fact(subject="medha-api", predicate="uses", object="PostgreSQL as primary datastore")
```

---

### `add-preference`
Record a categorised user preference explicitly (as opposed to burying it in a generic `remember`).

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `category` | string | yes | e.g. `"communication"`, `"testing"`, `"code-style"` |
| `preference` | string | yes | The preference text |
| `confidence` | number | no | 0.0–1.0 |
| `project` | string | no | |

```
add-preference(category="testing", preference="integration tests must hit a real DB, never mocked")
```

---

## Context & Sessions

### `get-context`
Assemble injection-ready context from memories, preferences, facts, conversation history, and slots — the "load everything relevant" entry point for session start.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `query` | string | no | Focus the retrieval |
| `sessionId` | string | no | Include session-scoped short-term memory |
| `includeShortTerm` | boolean | no | Include working memory stack |
| `includeLongTerm` | boolean | no | Include semantic/procedural memories |
| `includeSlots` | boolean | no | Include named pinned slots |
| `includeReasoning` | boolean | no | Include recent traces |
| `maxItems` | integer | no | Cap total items returned |
| `project` | string | no | |

```
get-context(query="Go API development", includeShortTerm=true, includeLongTerm=true, includeSlots=true)
```

---

### `get-conversation`
Retrieve the full stored conversation history for a session.

| Parameter | Type | Required |
|-----------|------|----------|
| `sessionId` | string | yes |

```
get-conversation(sessionId="sess_xyz")
```

---

### `session-history`
List recent sessions, most recent first. Useful for picking up where you left off.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `limit` | integer | no | 1–200 |
| `project` | string | no | |

```
session-history(limit=5, project="medha-api")
```

---

### `store-message`
Persist a single conversation message (user/assistant/system) into short-term memory for a session.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `sessionId` | string | yes | |
| `role` | enum | yes | `user`, `assistant`, `system` |
| `content` | string | yes | |
| `project` | string | no | |

```
store-message(sessionId="sess_xyz", role="user", content="focus on the auth middleware refactor")
```

---

## Working Memory Stack

A session-scoped LIFO stack for ephemeral in-flight context — scratch space that doesn't need to survive the session.

### `working-push`
Push an item onto the working memory stack.

| Parameter | Type | Required |
|-----------|------|----------|
| `sessionId` | string | yes |
| `content` | string | yes |

```
working-push(sessionId="sess_xyz", content="currently editing medha-api/internal/api/middleware.go")
```

---

### `working-pop`
Pop one or more items from the top of the stack.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `sessionId` | string | yes | |
| `count` | integer | no | Defaults to 1 |

```
working-pop(sessionId="sess_xyz", count=1)
```

---

### `working-clear`
Clear the entire working memory stack for a session.

| Parameter | Type | Required |
|-----------|------|----------|
| `sessionId` | string | yes |

```
working-clear(sessionId="sess_xyz")
```

---

## Actions & Orchestration

These tools model a multi-agent DAG of work items with dependency tracking.

### `action-create`
Create a new action (work item) in the orchestration DAG.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `title` | string | yes | |
| `description` | string | no | |
| `assigneeId` | string | no | Agent ID to assign to |
| `dependencies` | string[] | no | IDs of actions that must complete first |
| `status` | enum | no | `pending`, `running`, `completed`, `failed`, `cancelled` |
| `project` | string | no | |

```
action-create(title="Refactor auth middleware", dependencies=["action_001"], assigneeId="agent-A")
```

---

### `action-get`
Retrieve an action by ID.

| Parameter | Type | Required |
|-----------|------|----------|
| `id` | string | yes |
| `project` | string | no |

```
action-get(id="action_002")
```

---

### `action-update`
Update an action's status or reassign it.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `id` | string | yes | |
| `status` | enum | no | `pending`, `running`, `completed`, `failed`, `cancelled` |
| `assigneeId` | string | no | |
| `project` | string | no | |

```
action-update(id="action_002", status="completed")
```

---

### `next-action`
Return the single highest-priority unblocked action that is ready to execute (no unsatisfied dependencies).

| Parameter | Type | Required |
|-----------|------|----------|
| `project` | string | no |

```
next-action(project="medha-api")
```

---

### `frontier`
Return **all** unblocked actions ready to start simultaneously. Use this to identify parallelisable work.

| Parameter | Type | Required |
|-----------|------|----------|
| `project` | string | no |

```
frontier(project="medha-api")
```

---

### `checkpoint-create`
Create a named condition gate that blocks downstream actions until a boolean expression is satisfied.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `conditionExpr` | string | yes | Boolean expression string |
| `id` | string | no | Auto-generated when omitted |
| `project` | string | no | |

```
checkpoint-create(conditionExpr="tests_pass AND review_approved", id="gate_before_merge")
```

---

### `crystallize`
Compact a linear chain of completed actions into a single summary action — keeps the DAG tidy after long task sequences.

| Parameter | Type | Required |
|-----------|------|----------|
| `actionIds` | string[] | yes |
| `project` | string | no |

```
crystallize(actionIds=["action_001", "action_002", "action_003"])
```

---

### `lease-acquire`
Acquire an exclusive lease on an action to prevent two agents from running the same work concurrently. Returns a conflict error if another holder has it.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `actionId` | string | yes | |
| `holderId` | string | yes | This agent's ID |
| `ttlSecs` | integer | no | Defaults to 600 |
| `project` | string | no | |

```
lease-acquire(actionId="action_002", holderId="agent-A", ttlSecs=300)
```

---

### `lease-release`
Release a previously acquired exclusive lease.

| Parameter | Type | Required |
|-----------|------|----------|
| `actionId` | string | yes |
| `holderId` | string | no |
| `project` | string | no |

```
lease-release(actionId="action_002", holderId="agent-A")
```

---

## Tracing

Capture step-by-step reasoning for audit, debugging, and future reference.

### `start-trace`
Start a reasoning trace for a task.

| Parameter | Type | Required |
|-----------|------|----------|
| `sessionId` | string | yes |
| `task` | string | yes |
| `project` | string | no |

```
start-trace(sessionId="sess_xyz", task="Debug consolidation pipeline hang")
```

---

### `record-step`
Append a thought/action/observation step to an in-progress trace.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `traceId` | string | yes | |
| `thought` | string | yes | Internal reasoning |
| `action` | string | no | What was done |
| `observation` | string | no | What was observed |

```
record-step(traceId="trace_abc", thought="The hang is in the RabbitMQ consumer", action="inspected queue depth", observation="queue depth 0 — consumer not starting")
```

---

### `complete-trace`
Mark a trace as completed with an outcome summary.

| Parameter | Type | Required |
|-----------|------|----------|
| `traceId` | string | yes |
| `outcome` | string | no |
| `success` | boolean | no |

```
complete-trace(traceId="trace_abc", outcome="root cause: QUEUE_BACKEND env var missing", success=true)
```

---

## Skills & Learning

### `skill-upsert`
Record or increment evidence for a demonstrated skill. Call this when an agent successfully applies a skill to build a capability profile over time.

| Parameter | Type | Required |
|-----------|------|----------|
| `skillName` | string | yes |
| `project` | string | no |

```
skill-upsert(skillName="PostgreSQL migration authoring")
```

---

### `search-skills`
Search acquired skills by name.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `skillName` | string | no | Partial name match |
| `limit` | integer | no | |
| `project` | string | no | |

```
search-skills(skillName="PostgreSQL")
```

---

### `lesson-add`
Record a lesson learned from a session — things that were non-obvious, surprising, or worth avoiding next time.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `lesson` | string | yes | |
| `context` | string | no | Situation that produced this lesson |
| `sessionId` | string | no | |
| `project` | string | no | |

```
lesson-add(lesson="docker-compose base file inheritance breaks when fields conflict — prefer standalone files", context="docker-compose.local.yml debugging")
```

---

### `search-lessons`
Search lessons from past sessions by topic.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `topic` | string | no | |
| `limit` | integer | no | |
| `project` | string | no | |

```
search-lessons(topic="docker compose", limit=5)
```

---

### `patterns`
Detect and return recurring concept and file patterns across observations for a project. Useful for understanding what the agent (or codebase) spends most time on.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `project` | string | no | |
| `limit` | integer | no | 1–100 |
| `detect` | boolean | no | Re-run detection (slower but fresh) |

```
patterns(project="medha-api", detect=true, limit=20)
```

---

## Named Slots

Pinned memory slots are named persistent key-value pairs — think of them as typed bookmarks for frequently accessed context (persona, preferences, pending items, guidance).

### `slot-set`
Set a named pinned memory slot.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `slotName` | string | yes | e.g. `persona`, `preferences`, `pending_items`, `guidance` |
| `content` | string | yes | |
| `project` | string | no | |

```
slot-set(slotName="guidance", content="Always run go test ./... -race before marking a task done")
```

---

### `slot-get`
Retrieve a named pinned memory slot.

| Parameter | Type | Required |
|-----------|------|----------|
| `slotName` | string | yes |
| `project` | string | no |

```
slot-get(slotName="guidance")
```

---

### `slot-list`
List all named pinned memory slots for a project.

| Parameter | Type | Required |
|-----------|------|----------|
| `project` | string | no |

```
slot-list(project="medha-api")
```

---

## Signals (Inter-Agent Messaging)

### `signal-send`
Send a message from one agent to another.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `from` | string | yes | Sender agent ID |
| `to` | string | yes | Recipient agent ID |
| `subject` | string | no | |
| `body` | string | no | |
| `project` | string | no | |

```
signal-send(from="agent-A", to="agent-B", subject="handoff", body="auth middleware refactor complete, ready for review")
```

---

### `signal-inbox`
Read signals from an agent's inbox.

| Parameter | Type | Required |
|-----------|------|----------|
| `to` | string | yes |
| `project` | string | no |

```
signal-inbox(to="agent-B")
```

---

## Team Memory Sharing

### `team-share`
Share a memory with a team by publishing a pointer to the team feed.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `memoryId` | string | yes | |
| `team` | string | yes | Team name/ID |
| `mode` | enum | no | `read` (default) or `edit` |
| `actor` | string | no | Who is sharing |

```
team-share(memoryId="mem_abc123", team="backend", mode="read")
```

---

### `team-revoke`
Revoke a memory share from a team feed.

| Parameter | Type | Required |
|-----------|------|----------|
| `memoryId` | string | yes |
| `team` | string | yes |
| `actor` | string | no |

```
team-revoke(memoryId="mem_abc123", team="backend")
```

---

### `team-feed`
List all memories shared to a team, each entry including the full memory row.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `team` | string | yes | |
| `limit` | integer | no | 1–200 |

```
team-feed(team="backend", limit=20)
```

---

## Search & Discovery

### `timeline`
Chronological observations with cursor-based pagination. Supports filtering by session, hook type, and file path.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `project` | string | no | |
| `session` | string | no | Filter by session ID |
| `hookType` | string | no | Filter by hook type |
| `filePath` | string | no | Filter by file path |
| `after` | string | no | ISO-8601 cursor |
| `before` | string | no | ISO-8601 cursor |
| `limit` | integer | no | 1–200 |

```
timeline(project="medha-api", filePath="medha-api/internal/api/middleware.go", limit=50)
```

---

### `file-history`
Chronological list of compressed observations that touched a given file path.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `filePath` | string | yes | Exact match within the files array |
| `limit` | integer | no | 1–200 |
| `project` | string | no | |

```
file-history(filePath="medha-api/internal/consolidation/pipeline.go", limit=20)
```

---

### `profile`
Project intelligence snapshot: top concepts, top files, memory type distribution, and counts.

| Parameter | Type | Required |
|-----------|------|----------|
| `project` | string | no |

```
profile(project="medha-api")
```

---

### `facet-tag`
Tag a memory with a `dimension:value` facet for structured filtering.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `memoryId` | string | yes | |
| `dimension` | string | yes | e.g. `layer`, `status`, `component` |
| `value` | string | yes | |

```
facet-tag(memoryId="mem_abc123", dimension="layer", value="api")
facet-tag(memoryId="mem_abc123", dimension="status", value="confirmed")
```

---

### `facet-query`
Query memories by one or more `dimension:value` facet combinations.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `facets` | object | yes | Map of dimension → value list |
| `limit` | integer | no | 1–100 |
| `project` | string | no | |

```
facet-query(facets={"layer": ["api"], "status": ["confirmed"]}, limit=20)
```

---

## Entity Management

### `entity-duplicates`
List potential duplicate entity pairs pending review, ordered by confidence.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `project` | string | no | |
| `minConfidence` | number | no | 0.0–1.0, defaults to 0.7 |
| `limit` | integer | no | 1–500 |

```
entity-duplicates(minConfidence=0.85, limit=50)
```

---

### `entity-review-duplicate`
Confirm or reject a `SAME_AS` entity match. Confirming merges the entities.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `sourceId` | string | yes | |
| `targetId` | string | yes | |
| `confirm` | boolean | yes | `true` to merge, `false` to reject |

```
entity-review-duplicate(sourceId="ent_001", targetId="ent_002", confirm=true)
```

---

## Visual Memory

### `vision-embed`
Embed an image (by URL or caption text) into the vector index for visual memory search.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `docId` | string | yes | Unique identifier for this visual memory |
| `imageUrl` | string | no | URL of the image |
| `caption` | string | no | Text description; used when `imageUrl` is absent |
| `project` | string | no | |

```
vision-embed(docId="arch-diagram-v2", imageUrl="https://...", caption="System architecture diagram showing Go and Python services")
```

---

## Governance & Audit

### `audit`
Retrieve the audit log, optionally filtered by action type or actor.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `action` | string | no | e.g. `share`, `delete` |
| `actor` | string | no | |
| `limit` | integer | no | 1–500 |

```
audit(action="delete", limit=100)
```

---

### `governance-delete`
Compliance-safe hard-delete of one or more memories with a mandatory reason and full audit trail. Use this (not `forget`) when deletion must be explainable (e.g. GDPR, data retention policies).

| Parameter | Type | Required |
|-----------|------|----------|
| `memoryIds` | string[] | yes |
| `reason` | string | yes |
| `actor` | string | no |

```
governance-delete(memoryIds=["mem_abc123", "mem_def456"], reason="GDPR right-to-erasure request", actor="admin")
```

---

## Automation

### `routine-put`
Create or update a reusable workflow routine template — a named sequence of steps that can be replayed.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `name` | string | yes | |
| `steps` | string[] | no | Ordered step descriptions |
| `description` | string | no | |
| `id` | string | no | Auto-generated when omitted |
| `project` | string | no | |

```
routine-put(name="pre-merge checklist", steps=["run go test ./... -race", "run make lint", "verify smoke check endpoints"])
```

---

### `sentinel-create`
Create a sentinel that fires an HTTP callback when a matching event occurs in the system.

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `eventPattern` | string | yes | Pattern to match against events |
| `handlerUrl` | string | yes | HTTP URL to call when pattern matches |
| `id` | string | no | Auto-generated when omitted |
| `project` | string | no | |

```
sentinel-create(eventPattern="session.end", handlerUrl="http://localhost:9000/hooks/consolidate")
```

---

## System

### `status`
Report agent-mem health: counts of sessions, observations, and memories, plus schema version. Call this first if tools are behaving unexpectedly.

```
status()
```

---

### `diagnose`
Run full system health checks and return a diagnostic report (DB connectivity, queue depth, Python sidecar reachability, etc.).

```
diagnose()
```

---

### `export`
Export all memories and sessions for a project as a JSON bundle.

| Parameter | Type | Required |
|-----------|------|----------|
| `project` | string | no |

```
export(project="medha-api")
```

---

## Quick Reference

| Goal | Tool |
|------|------|
| Save something for later sessions | `remember` |
| Find relevant context | `smart-search` or `get-context` |
| Delete a memory | `forget` (simple) or `governance-delete` (compliance) |
| Store a user preference | `add-preference` |
| Store a structured fact | `add-fact` |
| Pin frequently needed values | `slot-set` / `slot-get` |
| Track in-flight scratch context | `working-push` / `working-pop` |
| Log reasoning steps | `start-trace` → `record-step` → `complete-trace` |
| Coordinate work across agents | `action-create` → `lease-acquire` → `action-update` |
| See what's ready to run | `next-action` or `frontier` |
| See history of a file | `file-history` |
| Check server health | `status` or `diagnose` |
| Share memory with a team | `team-share` |
| Message another agent | `signal-send` / `signal-inbox` |
