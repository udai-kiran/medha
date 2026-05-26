# Feature & Mechanism Analysis: Neo4j Agent Memory + agentmemory

## Executive Summary

**Neo4j Agent Memory** (DESIGN.md) is a graph-based persistent memory system with 3 memory tiers (Short/Long/Reasoning), semantic entity extraction (POLE+O), and multi-backend support (Neo4j Bolt + NAMS).

**agentmemory** (LOW_LEVEL_DESIGN.md) is a hook-based observation capture system with 4-tier memory consolidation (Working/Episodic/Semantic/Procedural), hybrid search (BM25 + Vector + Graph), and agent orchestration primitives (Actions, Leases, Routines, Signals).

These systems address **complementary problems**:
- Neo4j: *structured entity knowledge* with rich relationships
- agentmemory: *observation flow* with consolidation + decay

---

## Core Features by System

### Neo4j Agent Memory

**Memory Architecture:**
1. **Short-Term**: Conversation history, sequential message links, entity mentions
2. **Long-Term**: Entities (POLE+O), preferences, facts, enrichment metadata
3. **Reasoning**: Traces, steps, tool calls, decision audit trails

**Key Mechanisms:**
- Multi-stage extraction (spaCy → GLiNER → LLM fallback)
- Entity deduplication (exact + fuzzy + semantic matching)
- Relationship extraction and typed edges (WORKS_AT, KNOWS, LOCATED_IN, etc.)
- Geocoding for location entities (coordinates + spatial queries)
- Enrichment providers (Wikipedia, Diffbot with background processing)
- Preference pattern detection (regex + LLM)
- Conversation summarization
- TTL-based archival (move old conversations to separate tier)
- Multi-tenant support (per-user namespacing)
- Message linking (FIRST_MESSAGE → chain of NEXT_MESSAGE)
- 18 MCP tools (6 core + 12 extended + 4 Platinum/NAMS-only)
- Dual backends (Neo4j Bolt full-featured, NAMS REST API simplified)

**Data Model:**
- Entity: id, name, type (PERSON|OBJECT|LOCATION|EVENT|ORGANIZATION), subtype, embedding, confidence, enrichment fields
- Preference: category (food, music, travel), preference string, confidence
- Fact: subject-predicate-object triple
- Message: role, content, embedding, created_at, entities extracted
- Conversation: session_id, messages[], created_at, updated_at
- ReasoningTrace: task, steps[], tool_calls[], outcome, success
- Extractor provenance tracking (which tool extracted which entity)

**Indexes:**
- Vector indexes (1536 dims for embeddings)
- Text indexes (entity name, preference category, trace task)
- Unique constraints (IDs, tool names)
- Geospatial indexes (for location entities)

---

### agentmemory

**Memory Architecture:**
1. **Working Tier** (24h TTL): Raw observations, recent context
2. **Episodic Tier** (7d TTL): Session summaries, key decisions
3. **Semantic Tier** (unlimited, decay): Extracted facts, patterns, concepts
4. **Procedural Tier** (unlimited, decay): Workflows, decision patterns, reusable procedures

**Key Mechanisms:**
- Hook-based observation capture (12 lifecycle events: SessionStart, PostToolUse, PostToolFailure, SessionEnd, etc.)
- Real-time deduplication (5-min SHA-256 rolling window per session)
- Privacy filtering (strip OPENAI_API_KEY, password, <private> tags, ANSI codes)
- Dual compression paths:
  - **LLM path**: Extract facts + concepts + narrative + importance (requires API)
  - **Synthetic path**: BM25 tokenization + file extraction (zero-LLM, always available)
- Hybrid search with Reciprocal Rank Fusion (k=60):
  - BM25 (keyword search, stemming, synonym expansion)
  - Vector similarity (configurable dims 384–3072, providers: local/OpenAI/Gemini/Voyage)
  - Knowledge graph BFS (entity-relation traversal)
- Knowledge graph extraction (file paths, function names, concepts)
- Relationship types (implements, depends_on, related_to, contradicts, supersedes)
- Ebbinghaus-curve decay (strength *= 0.95^daysOld)
- 4-tier consolidation (SessionEnd hook triggers async pipeline)
- Team namespacing + shared feeds + inter-agent messaging (Signals)
- Orchestration primitives:
  - Actions (DAG work items with dependencies)
  - Leases (multi-agent exclusive locks)
  - Routines (workflow templates)
  - Sketches (ephemeral action graphs → promote to DAG)
  - Crystallization (compact action chains)
  - Sentinels (event-driven watchers)
  - Checkpoints (external condition gates)
- P2P mesh replication between instances
- Governance audit trail (every delete logged)
- Vision/image support (capture + search)
- Replay/timeline (chronological session playback)

**Data Model:**
- Session: id, project, cwd, startedAt, endedAt, status, observationCount, tags, summary
- RawObservation: id, sessionId, timestamp, hookType, toolName, toolInput, toolOutput, userPrompt, raw (pre-compression)
- CompressedObservation: id, sessionId, timestamp, type, title, subtitle, facts[], narrative, concepts[], files[], importance, confidence, modality (text|image|mixed)
- Memory: id, createdAt, type (pattern|preference|architecture|bug|workflow|fact), title, content, concepts[], files[], sessionIds[], strength (0–10), isLatest, sourceObservationIds[]
- SessionSummary: sessionId, project, title, narrative, keyDecisions[], filesModified[], concepts[], observationCount
- HookPayload: hookType, sessionId, project, cwd, timestamp, data {tool_name?, tool_input?, tool_output?, prompt?, assistant_response?, ...}

**State Storage (34 KV scopes):**
- `sessions:{project}[sessionId]` → Session metadata
- `observations:{project}[obsId]` → Raw or Compressed observation
- `memories:{project}[memoryId]` → Long-term memory
- `sessions:summary:{project}[sessionId]` → SessionSummary
- `graph:entities:{project}[entity]` → Entity adjacency list
- `graph:edges:{project}[source→target]` → Relationship metadata
- `bm25:index:{project}` → Inverted index (term → [docIds])
- `vector:index:{project}` → Embeddings (obsId → float32[])
- `actions:{project}[actionId]`, `leases:{actionId}`, `routines:{project}`, `signals:{agentId}`, etc.

---

## Comparative Feature Matrix

| Feature | Neo4j | agentmemory | Notes |
|---------|-------|-------------|-------|
| **Memory Tiers** | 3 (Short/Long/Reasoning) | 4 (Working/Episodic/Semantic/Procedural) | Different organization philosophies |
| **Entity Typing** | POLE+O explicit | Implicit (code symbols, files, concepts) | Neo4j more structured; agentmemory more flexible |
| **Entity Extraction** | spaCy → GLiNER → LLM | Regex (files) + NLP patterns + LLM | Neo4j more sophisticated |
| **Deduplication** | Exact + fuzzy + semantic | SHA-256 5-min rolling window | agentmemory: faster but shorter window |
| **Memory Decay** | TTL-based archival | Ebbinghaus curve (strength decay) + TTL | agentmemory: human-like forgetting |
| **Relationships** | Typed (WORKS_AT, KNOWS, etc.) | Inferred (depends_on, contradicts, supersedes) | Neo4j: explicit business logic; agentmemory: inferred |
| **Search** | Vector + Cypher graph query | BM25 + Vector + Graph (RRF fusion) | Both hybrid; agentmemory more algorithmic |
| **Consolidation** | Archival jobs | 4-tier pipeline (SessionEnd hook) | agentmemory more sophisticated |
| **Privacy** | None built-in (trust boundary at DB) | Automatic filtering (API keys, <private> tags) | agentmemory: defense in depth |
| **Capture Method** | Manual API calls | Hook-based (automatic from agent lifecycle) | agentmemory: zero-integration |
| **Multi-tenancy** | User management (per-user KV) | Team namespacing | Both supported, different models |
| **Observability** | Custom (schema management) | OTEL traces + metrics + health checks | agentmemory: production-ready |
| **Enrichment** | Wikipedia/Diffbot (background) | File history + profile snapshots | Neo4j: external knowledge; agentmemory: project-internal |
| **Orchestration** | None (scope: memory only) | Actions, Leases, Routines, Signals | agentmemory extends to agent coordination |
| **Decay Model** | TTL (sharp edge) | Strength score (gradual) | agentmemory more nuanced |
| **Backends** | Neo4j Bolt + NAMS REST | iii KV + SQLite only | Trade-off: flexibility vs. simplicity |
| **Tool Surface** | 18 MCP tools | 53 MCP + 124 REST + 123 iii functions | agentmemory more comprehensive |
| **Real-time UI** | None | Viewer (:3113) + iii console | agentmemory: observability included |

---

## Combinable Mechanisms

### 1. **Unified Entity Model**

**Neo4j provides:** Explicit POLE+O typing, rich metadata (enriched_description, wikipedia_url, wikidata_id, image_url), relationship predicates

**agentmemory provides:** Flexible extraction from code (functions, files, symbols), confidence-based weighting

**Combination:**
```
Entity {
  id, name, type (PERSON|OBJECT|LOCATION|EVENT|ORGANIZATION),
  subtype (optional),
  
  # Extracted from observations
  concepts: ["keyword1", "keyword2"],        # From agentmemory
  files: ["/src/auth.ts"],                   # From agentmemory
  
  # Rich context
  enriched_description: "...",               # From Neo4j
  wikipedia_url: "...",                      # From Neo4j
  image_url: "...",                          # From Neo4j
  
  # Lineage
  sourceObservationIds: ["obs-1", "obs-2"],  # From agentmemory
  extractorName: "GLiNEREntityExtractor",    # From Neo4j provenance
  
  # Decay
  strength: 0.85,                            # From agentmemory
  lastRetrieved: timestamp,                  # Ebbinghaus reinforcement
  
  embedding: float32[],
  confidence: 0.95,
  createdAt: timestamp,
  updatedAt: timestamp
}
```

---

### 2. **Multi-Tier Memory Consolidation**

**Neo4j approach:** Short (recent messages) → Long (extracted entities) → Reasoning (traces)

**agentmemory approach:** Working (24h raw) → Episodic (7d summaries) → Semantic (unlimited facts) → Procedural (workflows)

**Unified approach:**
```
Tier 1: WORKING (24h TTL)
  - RawObservations from hooks
  - Recent messages
  - No processing

Tier 2: EPISODIC (7d TTL, then archival)
  - CompressedObservations
  - SessionSummaries
  - ConversationSummaries
  - Linked entities + mentions

Tier 3: SEMANTIC (unlimited, strength decay)
  - Extracted facts (subject-predicate-object)
  - Patterns + concepts
  - Entity graph with relationships
  - Preferences
  - Reasoning traces

Tier 4: PROCEDURAL (unlimited, strength decay)
  - Workflows + routines
  - Decision patterns
  - Reusable procedures
  - Best practices
```

Consolidation triggers:
- SessionEnd hook → compress observations + summarize + extract facts
- Periodic jobs → detect superseded facts, apply decay, evict low-strength items

---

### 3. **Comprehensive Search Stack**

**Neo4j:** Vector embedding index + Cypher relationship traversal

**agentmemory:** BM25 + Vector + Graph (RRF fusion)

**Combined approach:**
```
Search Query: "authentication middleware implementation"

Stage 1: BM25 Keyword Search
  - Tokenize: ["authentication", "middleware", "implementation"]
  - Invert index: term → [obsIds, memoryIds]
  - Score by TF-IDF
  → Candidate set: 20 observations

Stage 2: Vector Similarity
  - Embed query → float32[384]
  - Cosine sim against all embeddings
  → Candidate set: 30 observations

Stage 3: Knowledge Graph Traversal
  - Extract entities from query: ["authentication", "middleware"]
  - Entity graph BFS: auth → depends_on → token validation → jwt
  - Return related observations mentioning JWT, validation, tokens
  → Candidate set: 15 observations

Stage 4: Reciprocal Rank Fusion (k=60)
  - Combine rankings from stages 1–3
  - Diversity boost (max 3 from same session)
  - Return top 10 with confidence scores
```

---

### 4. **Observation Capture → Extraction → Consolidation Pipeline**

**Current agentmemory flow:**
```
Hook → Validate + Dedup + Privacy Filter → Store RawObservation
  ↓ (async)
Compress (LLM or synthetic) → Index (BM25 + Vector) → Store CompressedObservation
  ↓ (SessionEnd hook)
Consolidate (4-tier) → Extract entities + relationships → Store Memory
  ↓ (periodic)
Decay (Ebbinghaus) + TTL eviction
```

**Enhanced with Neo4j extraction:**
```
Hook → Validate + Dedup + Privacy Filter → Store RawObservation
  ↓ (async, configurable LLM providers)
Compress:
  - Vision: LLM image description (if multimodal)
  - Text: spaCy NER → GLiNER → LLM fallback
  - Extract: facts, concepts, narrative, importance
  → Store CompressedObservation
  ↓
Index: BM25 + Vector + Graph
  ↓ (SessionEnd hook)
Consolidate:
  - Summarize observations → SessionSummary
  - Extract entities → create/merge Entity nodes
  - Build relationships (WORKS_AT, DEPENDS_ON, etc.)
  - Detect duplicates → dedup + merge
  - Enrich entities (Wikipedia, project knowledge)
  - Cluster observations → Memory (by semantic group)
  - Assign POLE+O types + confidence
  → Store Memory + SessionSummary + Entity graph
  ↓
Query/Retrieve:
  - Hybrid search (BM25 + Vector + Graph RRF)
  - Context injection (Neo4j: relationship traversal depth + entity enrichment)
  - Ebbinghaus decay (strength reinforcement on retrieval)
```

---

### 5. **Relationship Extraction & Graph Building**

**Neo4j approach:** Explicit typed relationships (WORKS_AT, KNOWS, LOCATED_IN)

**agentmemory approach:** Inferred relationships from extraction + code analysis

**Combined approach:**
```
Relationship Types (merged):
- WORKS_AT (Person → Organization)
- KNOWS (Person → Person)
- MEMBER_OF (Person → Organization)
- LOCATED_IN (Entity → Location)
- DEPENDS_ON (Code entity → Code entity)
- IMPLEMENTS (Function → Requirement)
- RELATED_TO (Generic, confidence-weighted)
- CONTRADICTS (Fact → Fact, conflicting)
- SUPERSEDES (Fact → older Fact)
- DERIVED_FROM (Memory → RawObservation)

Graph Building Pipeline:
1. Extract entities: spaCy (high-conf) + GLiNER (med-conf) + LLM (fallback)
2. Type them: PERSON, ORGANIZATION, LOCATION, OBJECT (code files), EVENT
3. Extract relationships:
   - Dependency parsing (code structure)
   - Pattern matching (WORKS_AT, LIVES_IN)
   - LLM relation extraction
4. Build edges with confidence + source provenance
5. Store in Neo4j or KV graph:
   edges:{project}[source→target] = {type, confidence, timestamp, source_obsId}

Query Graph:
- BFS traversal (relationship_depth configurable)
- Confidence filtering (min 0.7)
- Time-bounded queries (last 30 days)
- Type filtering (only PERSON relationships, etc.)
```

---

### 6. **Multi-Tier Decay & Retention Strategy**

**Neo4j:** TTL-based archival (sharp edges)

**agentmemory:** Ebbinghaus curve (smooth decay)

**Combined approach:**
```
Memory Lifecycle:

WORKING (24h): Raw observations
  - No decay
  - Full fidelity
  - TTL: 24h hard eviction

EPISODIC (7d): Session summaries + compressed observations
  - No decay during 7d window
  - TTL: 7d → archive to separate neo4j label (:ArchivedConversation)
  - Strength: 1.0 (max)

SEMANTIC (unlimited): Extracted facts + patterns
  - Strength decay: strength *= 0.95^daysOld
  - Reinforcement on retrieval: strength += 0.5 (capped at 1.0)
  - Eviction threshold: strength < 0.1 → mark for deletion
  - Review window: strength 0.1–0.3 → human review before eviction

PROCEDURAL (unlimited): Workflows + decision patterns
  - Same decay as SEMANTIC
  - Manual retention boost (mark as "always keep")
  - Supersession tracking (newer procedure supersedes older)

Consolidation Timeline:
- T+0: Observation captured (hook)
- T+0–500ms: Compressed (async)
- T+sessionEnd: Consolidate (4-tier pipeline)
  - Working → Episodic (session summary)
  - Compressed observations → Semantic (facts extracted)
  - New patterns → Procedural (reusable procedures)
- T+1d: Auto-cleanup
  - Working tier: delete obs older than 24h
- T+7d: Archival
  - Episodic → Archive (soft delete in Neo4j)
- T+365d (nightly decay job):
  - Semantic/Procedural: apply Ebbinghaus curve
  - Delete if strength < 0.1
```

---

### 7. **Multi-Tenant Team Collaboration**

**Neo4j:** Per-user KV namespacing + user management

**agentmemory:** Team namespacing + shared feeds + signals

**Combined approach:**
```
Namespace Hierarchy:
  global:                          # Shared by all agents in instance
    observations, memories, graph
  
  team:{teamId}:                   # Team-shared
    shared_memories
    feed (audit trail of shared items)
    signals (inter-agent inbox)
  
  user:{userId}:                   # User-private
    observations, memories
    working_memory (stack)
    slots (pinned memories)

Permissions:
  - observation: always private to creator
  - memory: can be marked "team" or "private"
  - shared_memory: {sharedBy, mode: "read"|"edit", sharedAt}
  
  - Team feed: recent shared + audit events
  - Signals: inter-agent messaging with receipts
  - Leases: exclusive action assignment
  - Routines: shareable workflow templates

Example Workflow:
1. Agent-A creates memory: "Use jose for JWT" (private)
2. Agent-A manually shares with team
3. Team feed updates (real-time broadcast via Signal or webhook)
4. Agent-B recalls from team memory
5. Team audit trail: "Agent-A shared 'Use jose...' with Agent-B (2026-05-26T14:00Z)"
```

---

### 8. **Reasoning Memory as Orchestration Trace**

**Neo4j:** ReasoningTrace → ReasoningStep → ToolCall (audit trail)

**agentmemory:** Action DAG → Leases → Signals (agent coordination)

**Combined approach:**
```
ReasoningTrace:
  id: "trace-abc123"
  sessionId: "sess-xyz789"
  task: "Implement authentication middleware"
  initiatedBy: "user_prompt"
  startedAt: timestamp
  completedAt: timestamp (optional)
  success: boolean
  outcome: summary
  steps: [step1, step2, ...]
  metrics: {toolCount, avgStepTime, totalTime}

  # NEW: Link to orchestration
  relatedActions: ["action-1", "action-2"]
  leaseId: "lease-abc"  # If multi-agent
  signalsReceived: ["signal-1", "signal-2"]
  dependsOnCheckpoints: ["checkpoint-auth-ready"]

ReasoningStep:
  id: "step-1"
  traceId: "trace-abc123"
  index: 1
  thought: "Need to check authentication requirements"
  action: "search_memories('authentication')"
  observation: "Found 3 relevant memories about JWT patterns"
  timestamp: timestamp
  toolCalls: [toolCall1, toolCall2]
  touchedEntities: [{entity_id, relation: "QUERIED"}]
  
  # NEW: Sketch/Action mapping
  sketchId: "sketch-1"  # If part of ephemeral plan
  crystallizedInto: "action-2"  # If later compacted

ToolCall:
  id: "tool-1"
  stepId: "step-1"
  toolName: "memory_recall"
  arguments: {query: "authentication"}
  result: [{memory_id, relevance}]
  status: "SUCCESS"
  executionTime: 245  # ms
  createdAt: timestamp
  
  # NEW: Side effects
  modifiedEntities: ["entity-123"]
  createdMemories: ["mem-456"]

# Crystallization: compact multiple steps into single Memory
CrystallizedProcedure:
  id: "proc-auth-flow"
  sourceSteps: ["step-1", "step-2", "step-3", "step-4"]
  sourceTrace: "trace-abc123"
  procedureType: "workflow"
  title: "JWT authentication implementation"
  content: "Steps: 1) check requirements 2) select jose 3) implement middleware 4) test"
  strength: 1.0
  createdAt: timestamp
```

---

### 9. **Privacy & Security Integration**

**Neo4j:** None (trust boundary at database access)

**agentmemory:** Automatic privacy filtering

**Combined approach:**
```
Privacy Filter (multi-layer):

Layer 1: Observation Capture (agentmemory)
  - Strip secrets before storing RawObservation
  - Patterns: OPENAI_API_KEY=, password=, token=, secret=
  - Tags: <private>...</private> (entire block removed)
  - ANSI codes: removed
  
Layer 2: Entity Extraction (Neo4j)
  - If entity.metadata contains secret patterns: flag as "sensitive"
  - Don't enrich sensitive entities from external APIs
  
Layer 3: Search Results (Both)
  - If result contains sensitive entity: filter or mask
  - User prompt: "Don't return API keys" → enforced in search results
  
Layer 4: Access Control (Team namespacing + Neo4j security)
  - Observation always private to creator
  - Memory can be marked "private" (not shared)
  - Team-shared memories have explicit permissions
  - Neo4j per-user KV scopes (if multi-tenant)

Example:
RawObservation before filtering:
  {tool_name: "api_call", tool_output: "OPENAI_API_KEY=sk-1234567890abcdef"}

After privacy filter:
  {tool_name: "api_call", tool_output: "OPENAI_API_KEY=***REDACTED***"}

CompressedObservation:
  facts: ["Made API call (details redacted)"]
  (LLM extraction skipped for sensitive data)
```

---

### 10. **Integrated Tool & API Surface**

**Neo4j:** 18 MCP tools (client SDK API)

**agentmemory:** 53 MCP + 124 REST + 123 iii functions

**Combined approach:**
```
Unified Tool Registry:

Memory Tools (Read):
  memory_recall({query, project, limit}) → [Memory]
  memory_search({query, limit}) → [observation|memory]
  memory_smart_search({query, limit, mode}) → [hybrid results + ranking]
  memory_get_context({sessionId}) → formatted string
  memory_get_entity({entityId, depth}) → Entity + relationships
  memory_get_conversation({sessionId}) → Conversation + entities
  memory_get_graph({entities?, time_range?}) → {nodes, edges}
  memory_file_history({filePath}) → [observation]
  memory_relations({obsId}) → related nodes + graph
  memory_timeline({sessionId?}) → [observation] (chronological)

Memory Tools (Write):
  memory_store_message({sessionId, content, role}) → Message + auto-extracted entities
  memory_add_entity({name, type, subtype, description}) → Entity
  memory_add_preference({category, preference, confidence}) → Preference
  memory_add_fact({subject, predicate, object}) → Fact
  memory_create_relationship({source_id, target_id, type, confidence}) → Relationship
  memory_remember({type, title, content, concepts, files}) → Memory
  memory_start_trace({sessionId, task}) → ReasoningTrace
  memory_record_step({traceId, thought, action, observation}) → ReasoningStep
  memory_record_tool_call({stepId, tool_name, arguments, result}) → ToolCall

Orchestration Tools (agentmemory + Reasoning from Neo4j):
  memory_action_create({title, dependencies?}) → Action
  memory_next({project}) → {nextAction + context}
  memory_frontier({project}) → [unblocked actions + priority]
  memory_lease_acquire({actionId, agentId, durationMs}) → Lease
  memory_routine_run({routineId, params}) → Execution
  memory_signal_send({targetAgentId, message}) → Signal
  memory_checkpoint({condition}) → {ready: bool}
  memory_sketch_create({actionId[]}) → Sketch
  memory_sketch_promote({sketchId}) → Routine
  memory_crystallize({stepIds[]}) → Memory (compacted procedure)

Consolidation Tools:
  memory_consolidate({sessionId?}) → SessionSummary + Memories created
  memory_extract_lessons({sessionId}) → [Lesson]
  memory_auto_forget({project, force?}) → {evicted: int}
  memory_decay_apply({project}) → {decayed: int}

Team Tools:
  memory_team_share({memoryIds[], teamId, mode}) → {shared: int}
  memory_team_feed({teamId, limit?}) → [shared items]
  memory_audit({project?, action?}) → [audit entries]

Graph Tools:
  memory_graph_query({entities[], mode}) → {entities, edges, traversal}
  memory_graph_stats({project}) → {nodes, edges, density}
  memory_temporal_graph({timeRange}) → graph with timestamps

Diagnostics:
  memory_profile({project}) → {topConcepts[], topFiles[], patterns[]}
  memory_health() → {status, issues}
  memory_metrics({project}) → token usage, API costs
```

---

## Implementation Priorities (If Combining)

### **Phase 1: Core Integration** (would unify entity & search)
1. Unified entity model (Neo4j POLE+O + agentmemory concept extraction)
2. Hybrid search (BM25 + Vector + Graph RRF fusion)
3. Joint deduplication (exact + fuzzy + semantic confidence)

### **Phase 2: Memory Consolidation** (would unify tiers)
4. Multi-tier consolidation (Working/Episodic/Semantic/Procedural)
5. Ebbinghaus decay applied to Neo4j entities + memories
6. SessionEnd → consolidation pipeline (compress + extract + build graph)

### **Phase 3: Orchestration** (would extend Neo4j with action coordination)
7. ReasoningTrace linked to Action DAG
8. Lease + Signal integration for multi-agent traces
9. Crystallization: compact multi-step traces into reusable procedures

### **Phase 4: Team Collaboration** (would integrate social features)
10. Team namespacing over Neo4j multi-tenant schema
11. Shared memory feeds + signals (inter-agent messaging)
12. Governance audit trail (all mutations + deletions logged)

---

## Key Trade-offs

| Aspect | Pro | Con |
|--------|-----|-----|
| **Unified Entity Model** | Rich POLE+O + flexible extraction | More complex schema |
| **Multi-Tier Memory** | Mirrors human cognition | 4 tiers may be overkill for some use cases |
| **Hybrid Search (RRF)** | Combines keyword + semantic + graph | Higher latency (all three path required) |
| **Hook-Based Capture** | Zero-integration, automatic | Only works if agent integrates hooks |
| **Neo4j Backend** | ACID guarantees, rich query language | External DB dependency, ops overhead |
| **Ebbinghaus Decay** | Automatic forgetting, realistic | Requires periodic background job |
| **Orchestration Layer** | Agent coordination primitives | Adds complexity if unused |

---

## Recommendation

**Best combined approach** for a new system:
1. Start with **agentmemory's observation capture** (hook-based, automatic)
2. Add **Neo4j's extraction pipeline** (spaCy → GLiNER → LLM for rich entity typing)
3. Implement **hybrid search** (BM25 + Vector + Graph RRF)
4. Use **4-tier consolidation** (agentmemory model, better human-like forgetting)
5. Apply **Ebbinghaus decay** to all semantic memories
6. Extend with **orchestration** (Actions, Leases, Routines, Signals) for multi-agent scenarios
7. Layer **team namespacing + governance** for collaboration
8. Provide **both Neo4j Bolt and iii KV backends** (flexibility in deployment)

This combination would yield:
- **Automatic capture** (zero integration burden)
- **Rich entities** (POLE+O with extraction provenance)
- **Powerful search** (3-path hybrid fusion)
- **Human-like memory** (decay + reinforcement)
- **Agent orchestration** (Actions, coordination primitives)
- **Team-ready** (shared memories, signals, audit trail)
