# agentmemory: Low-Level Design Document

## Executive Summary

**agentmemory** is a persistent memory system for AI coding agents built on the **iii** (interoperability integration interface) engine. It provides 53 tools across MCP, REST API, and iii function registrations to capture, compress, search, and organize observations from agent sessions into a semantic knowledge base with 4-tier memory consolidation.

**Key stats:**
- 123 iii functions registered
- 34 KV state scopes
- 950+ unit tests
- 0 external database dependencies (SQLite + iii KV)
- 95.2% retrieval R@5 (LongMemEval-S)

---

## System Architecture

### High-Level Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                      Agent Clients                              │
│  (Claude Code, Cursor, Cline, OpenCode, Hermes, etc.)          │
└──────────┬──────────────────────────────────────────────────────┘
           │ Hooks (12 lifecycle events)
           │ MCP Protocol (53 tools)
           │ REST API (124 endpoints)
           ▼
┌─────────────────────────────────────────────────────────────────┐
│              agentmemory Service (Node.js)                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Core Memory Pipeline                                     │  │
│  │  • Observe: Raw observation capture (PostToolUse hook)   │  │
│  │  • Dedup: 5-min SHA-256 window dedup                     │  │
│  │  • Privacy: Strip secrets, API keys, <private> tags      │  │
│  │  • Compress: LLM or synthetic BM25 compression           │  │
│  │  • Embed: Vector embeddings (6 providers + local)        │  │
│  │  • Index: BM25 + vector + knowledge graph                │  │
│  │  • Consolidate: 4-tier memory lifecycle                  │  │
│  │  • Decay: Ebbinghaus-curve based memory eviction         │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ State Management (iii KV + SQLite)                       │  │
│  │  • Sessions (id, project, cwd, status, metadata)         │  │
│  │  • Raw Observations (raw data + hook type + timestamps)  │  │
│  │  • Compressed Observations (structured facts + narrative)│  │
│  │  • Memories (long-term facts, patterns, workflows)       │  │
│  │  • Sessions Summaries (episodic consolidation)           │  │
│  │  • Vector Index (in-memory + persistent)                 │  │
│  │  • Knowledge Graph (entities + relationships)            │  │
│  │  • Team/Private namespacing                              │  │
│  │  • Leases, Actions, Routines, Checkpoints               │  │
│  │  • Audit Trail (all mutations with timestamps)           │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Search & Retrieval                                       │  │
│  │  • BM25: Stemmed keyword + synonym expansion             │  │
│  │  • Vector: Cosine similarity over embeddings             │  │
│  │  • Graph: Entity matching + RDF traversal                │  │
│  │  • Fusion: Reciprocal Rank Fusion (k=60) + diversity     │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ Orchestration Layer                                      │  │
│  │  • Actions: DAG-like work items with dependencies        │  │
│  │  • Frontier: Unblocked actions ranked by priority        │  │
│  │  • Leases: Exclusive multi-agent action assignment       │  │
│  │  • Routines: Repeatable workflow templates               │  │
│  │  • Signals: Inter-agent messaging with receipts          │  │
│  │  • Checkpoints: External condition gates                 │  │
│  │  • Sketches: Ephemeral action graphs (promote to DAG)    │  │
│  │  • Crystallize: Compact action chains                    │  │
│  │  • Mesh: P2P sync between agentmemory instances          │  │
│  │  • Sentinels: Event-driven watchers                      │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ iii Engine Integration (WebSocket)                       │  │
│  │  • Worker registration + telemetry                       │  │
│  │  • Function triggers (HTTP, events, state, cron)         │  │
│  │  • KV state scopes (isolated per project/session)        │  │
│  │  • Streams (real-time observation flow)                  │  │
│  │  • OTEL traces + metrics (per-function spans)            │  │
│  │  • Queues (durable embedding/compression jobs)           │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
           ▲                                    ▲
           │ REST API (:3111)                   │ WebSocket Streams
           │ MCP Tools                          │ Real-time Viewer
           │                                    │
┌──────────┴─────────────────┐    ┌────────────┴──────────────────┐
│   Client Application       │    │   Viewer / Console UI          │
│   (REST / MCP consumers)   │    │   (:3113 / iii-console)       │
└────────────────────────────┘    └────────────────────────────────┘
```

### Core Components

#### 1. **Worker Registration (iii SDK)**
- **File:** `src/index.ts`
- **Entry point:** `registerWorker(config.engineUrl, {...})`
- Connects to iii engine at `ws://localhost:49134` (configurable)
- Registers agentmemory as a "worker" with OpenTelemetry telemetry
- Invocation timeout: 180 seconds (for long consolidation jobs)

#### 2. **State Management (StateKV + SQLite)**
- **File:** `src/state/kv.ts`, `src/state/schema.ts`
- Wraps iii's KV state with typed operations
- 34 named KV scopes (per-project, per-session, global)
- Persistence via SQLite (`.agentmemory/data.db`)
- Dedup map for observation window (5-min rolling SHA-256)
- Metrics store for token budgets and API usage

#### 3. **Vector Index & Hybrid Search**
- **Files:** `src/state/vector-index.ts`, `src/state/hybrid-search.ts`, `src/functions/search.ts`
- BM25 index (stemming, synonym expansion, CJK tokenization)
- Vector index (float32 embeddings, configurable dimensions 384–3072)
- Reciprocal Rank Fusion fusion (k=60)
- Graph traversal (entity extraction + relationship BFS)
- Persistent storage (SQLite serialization)

#### 4. **Privacy & Observation Dedup**
- **File:** `src/functions/privacy.ts`, `src/functions/dedup.ts`
- Strip ANSI codes, API keys, secrets
- `<private>` tag filtering
- SHA-256 dedup over 5-minute window
- Observation type classification (file_read, command_run, etc.)

#### 5. **Compression Pipeline**
- **Files:** `src/functions/compress.ts`, `src/functions/compress-synthetic.ts`
- **LLM-backed:** Extracts facts + concepts + narrative (requires API key)
- **Synthetic:** BM25 tokenization + frequency ranking (zero-LLM, always available)
- Returns `CompressedObservation` with:
  - `facts[]` – extractive bullet points
  - `narrative` – concise summary
  - `concepts[]` – semantic tags
  - `importance` – relevance score (0–1)
  - `confidence` – compression quality (0–1)

#### 6. **Memory Consolidation Pipeline (4-tier)**
- **File:** `src/functions/consolidation-pipeline.ts`
- Fires on `SessionEnd` hook (async, non-blocking)
- **Working Tier:** Raw observations (24h TTL)
- **Episodic Tier:** Session summaries (7d TTL)
- **Semantic Tier:** Extracted facts + patterns (unlimited, decay via Ebbinghaus)
- **Procedural Tier:** Workflows + decision patterns (unlimited, manual pruning)
- Detects contradictions, supersessions, relationships
- Updates `Memory` records with versioning + strength scores

#### 7. **Knowledge Graph**
- **File:** `src/functions/graph.ts`, `src/functions/graph-retrieval.ts`
- Entity extraction: named entities, code symbols, file paths
- Relationship types: implements, depends_on, related_to, contradicts, supersedes
- Storage: edge list + adjacency in KV state
- Retrieval: BFS traversal from query entities
- Integrated into hybrid search (RRF fusion)

#### 8. **Embedding Providers**
- **File:** `src/providers/index.ts`
- Auto-detection chain:
  1. `EMBEDDING_PROVIDER=local` → Xenova `all-MiniLM-L6-v2` (free, offline)
  2. `GEMINI_API_KEY` → Gemini `gemini-embedding-001` (100+ languages)
  3. `OPENAI_API_KEY` → OpenAI `text-embedding-3-small` ($0.02/1M)
  4. `VOYAGE_API_KEY` → Voyage AI `voyage-code-3` (code-optimized)
  5. `OPENROUTER_API_KEY` → Multi-model proxy
  6. None configured → BM25-only mode

#### 9. **LLM Providers**
- **File:** `src/providers/index.ts`
- Auto-detection:
  1. `AGENTMEMORY_ALLOW_AGENT_SDK=true` → `@anthropic-ai/claude-agent-sdk` (Claude subscription, opt-in to avoid #149)
  2. `ANTHROPIC_API_KEY` → Anthropic API (per-token billing)
  3. `GEMINI_API_KEY` → Google Gemini
  4. `OPENAI_API_KEY` → OpenAI (with Azure support)
  5. `OPENROUTER_API_KEY` → OpenRouter proxy
  6. No provider → LLM operations disabled (compress, summarize fall back to synthetic)
- Fallback chain: primary + 2 backup providers for resilience

#### 10. **Plugin Hooks (Lifecycle Events)**
- **Files:** Hook integration via agent-specific plugins
- **12 hooks captured:**
  - `SessionStart` – session initialization
  - `UserPromptSubmit` – user input
  - `PreToolUse` – file access patterns
  - `PostToolUse` → observe() [main data capture]
  - `PostToolUseFailure` – error context
  - `PreCompact` – memory injection before context shrinking
  - `SubagentStart/Stop` – sub-agent lifecycle
  - `Stop` – end-of-session summary
  - `SessionEnd` → consolidate() [4-tier pipeline]

#### 11. **HTTP Triggers (REST API)**
- **File:** `src/triggers/api.ts` (2,757 lines)
- **124 endpoints** on `:3111`
- Middleware: Authorization (Bearer token if `AGENTMEMORY_SECRET` set)
- Response format: `{ status_code, body, headers? }`

#### 12. **MCP Server Integration**
- **Files:** `src/mcp/server.ts`, `src/mcp/tools-registry.ts`
- Exports 53 MCP tools (configurable `AGENTMEMORY_TOOLS=core|all`)
- 6 MCP resources
- 3 MCP prompts
- 4 slash skills (`/recall`, `/remember`, `/session-history`, `/forget`)
- MCP shim package (`@agentmemory/mcp`) proxies to running server or falls back to 7 core tools

#### 13. **Real-Time Viewer**
- **File:** `src/viewer/server.ts`
- Runs on `:3113` (loopback-bound, SSH tunnel for remote)
- Live observation stream, session explorer, memory browser
- Knowledge graph visualization
- Health dashboard + metrics
- CSP headers + per-response nonce

---

## Memory Ingestion Pipeline (Detailed)

### Phase 1A: Receive Hook Payload

**Entry Point:** Agent (Claude Code, Cursor, etc.) fires hook

```
HookPayload arrives at REST endpoint: POST /agentmemory/observe
  ↓
registerObserveFunction() handler
  ↓
mem::observe iii function triggered
```

**Payload example (PostToolUse hook):**
```json
{
  "hookType": "post_tool_use",
  "sessionId": "sess-abc123",
  "project": "myproject",
  "cwd": "/home/user/myproject",
  "timestamp": "2026-05-26T12:34:56.789Z",
  "data": {
    "tool_name": "read",
    "tool_input": {"file_path": "/src/auth.ts"},
    "tool_output": "export function validateToken..."
  }
}
```

### Phase 1B: Validation & Dedup

**Function:** `mem::observe` (lines 43–281 in `src/functions/observe.ts`)

```typescript
Input: HookPayload {
  sessionId, hookType, timestamp, data: {...}
}
  ↓
Step 1: Validate required fields
  - sessionId: required (string)
  - hookType: required (string, one of 12 types)
  - timestamp: required (ISO 8601)
  - Return error if missing
  ↓
Step 2: Generate observation ID
  - obsId = generateId("obs")  // "obs-xyz789..."
  ↓
Step 3: Dedup check (5-minute window)
  - IF dedupMap enabled:
    - toolName = data.tool_name || hookType
    - dedupHash = SHA-256(sessionId + toolName + JSON.stringify(toolInput))
    - IF dedupHash in dedupMap.current5minWindow:
      - RETURN { deduplicated: true, sessionId }  // Skip
    - Else: record hash in dedupMap
  ↓
Step 4: Privacy filter
  - JSON.stringify(data) → sanitizedStr
  - stripPrivateData(sanitizedStr)
    - Remove: OPENAI_API_KEY=, password=, token=, secret=, authorization:, bearer
    - Remove: <private>...</private> blocks
  - JSON.parse(sanitizedStr) → sanitizedRaw
```

### Phase 1C: Raw Observation Storage

```typescript
Step 5: Create RawObservation
  ↓
RawObservation = {
  id: obsId,                             // "obs-xyz789"
  sessionId: payload.sessionId,          // "sess-abc123"
  timestamp: payload.timestamp,          // "2026-05-26T12:34:56Z"
  hookType: payload.hookType,            // "post_tool_use"
  raw: sanitizedRaw,                     // Full payload (privacy-filtered)
  
  // Extracted fields (if post_tool_use/post_tool_failure):
  toolName: data.tool_name,              // "read"
  toolInput: data.tool_input,            // {...}
  toolOutput: data.tool_output,          // "export function..."
  
  // Extracted fields (if prompt_submit):
  userPrompt: data.prompt,               // User's input text
  
  // Image handling:
  modality: undefined | "image" | "text" | "mixed",
  imageData: undefined | filePath | base64,
}
  ↓
Step 6: Image extraction & storage
  - IF imageData found in raw:
    - Save to ~/.agentmemory/images/{hash}.{ext}
    - raw.imageData = filePath
    - Increment image refcount
    - OPTIONAL: trigger vision embedding (if AGENTMEMORY_IMAGE_EMBEDDINGS=true)
  ↓
Step 7: Store RawObservation in KV
  - await kv.set(KV.observations(sessionId), obsId, RawObservation)
  - Location: `observations:{project}[obsId]`
  - ACID guarantee: iii's transactional KV
  ↓
Step 8: Update Session metadata
  - session.observationCount += 1
  - session.updatedAt = now()
  - IF session.firstPrompt empty and hookType="prompt_submit":
    - session.firstPrompt = data.prompt.slice(0, 200)
  - await kv.update(KV.sessions, sessionId, [{path: "observationCount", value: count+1}, ...])
  ↓
Step 9: Broadcast to viewers (real-time streams)
  - Trigger stream::set (session stream)
    - stream_name: "agentmemory"
    - group_id: session-specific
    - data: { type: "raw", observation: RawObservation }
  - Trigger stream::send (viewer broadcast)
    - group_id: "agentmemory:viewers"
    - data: { type: "raw_observation", observation, sessionId }
```

**KV State after Phase 1:**
```
observations:{project}[obsId] = RawObservation {
  id: "obs-xyz789",
  sessionId: "sess-abc123",
  timestamp: "2026-05-26T12:34:56Z",
  hookType: "post_tool_use",
  toolName: "read",
  toolInput: {...},
  toolOutput: "...",
  raw: {...}  (privacy-filtered)
}

sessions[sess-abc123].observationCount = 42
sessions[sess-abc123].updatedAt = "2026-05-26T12:34:56Z"
```

### Phase 2: Compression (Async, ~100–500ms)

**Two paths:** LLM-powered or synthetic (configurable)

#### **Path A: LLM Compression** (if `AGENTMEMORY_AUTO_COMPRESS=true`)

**Function:** `mem::compress` (async trigger, no blocking)

```typescript
Input: {
  observationId: "obs-xyz789",
  sessionId: "sess-abc123",
  raw: RawObservation
}
  ↓
Step 1: Vision image description (optional)
  - IF raw.modality = "image" or "mixed":
    - IF raw.imageData provided and provider.describeImage exists:
      - Call provider.describeImage(base64, mimeType, VISION_DESCRIPTION_PROMPT)
      - imageDescription = "A screenshot showing the login form..."
  ↓
Step 2: Prepare compression prompt
  - SYSTEM: COMPRESSION_SYSTEM (detailed extraction instructions)
  - USER: buildCompressionPrompt(raw, imageDescription)
    - Format: "Analyze this tool invocation: TOOL=read INPUT=... OUTPUT=... PROMPT=..."
  ↓
Step 3: Call LLM (with retry logic)
  - provider.compress(systemPrompt, userPrompt)
    - Model: claude-opus-4 (configurable)
    - Max tokens: 2000
    - Timeout: 60s
    - ON TIMEOUT: Fall back to synthetic compression
  ↓
Step 4: Parse LLM response (XML extraction)
  - Response format: <type>file_read</type><title>Read auth.ts</title>...
  - parseCompressionXml(response)
    - Extract: type, title, subtitle, facts[], narrative, concepts[], files[], importance
    - Validate: type in VALID_TYPES set
    - Clamp: importance 1–10
  ↓
Step 5: Quality evaluation
  - validateOutput(parsed) against CompressOutputSchema
  - scoreCompression(parsed) → confidence 0–1
  - confidence < 0.5: retry with self-correction
  ↓
Step 6: Build CompressedObservation
  - CompressedObservation = {
      id: obsId,                         // "obs-xyz789"
      sessionId: "sess-abc123",
      timestamp: raw.timestamp,
      type: "file_read",                 // From <type> tag
      title: "Read auth.ts",             // From <title> tag
      subtitle: "src/middleware/auth.ts", // From <subtitle> tag
      facts: [                           // From <facts> children
        "Implements JWT token validation",
        "Uses jose library for Edge compatibility"
      ],
      narrative: "Examined authentication middleware...",  // From <narrative> tag
      concepts: ["authentication", "jwt", "edge"],         // From <concepts> children
      files: ["src/middleware/auth.ts"],                   // From <files> children
      importance: 7,                     // From <importance> tag, 1–10
      confidence: 0.85,                  // Quality score from LLM
      imageDescription: "...",
      imageData: "/path/to/image",
      modality: "text"
    }
```

#### **Path B: Synthetic Compression** (default, `AGENTMEMORY_AUTO_COMPRESS=false`)

**Function:** `buildSyntheticCompression(RawObservation)` (inline, synchronous)

```typescript
Input: RawObservation {
  id, sessionId, timestamp,
  hookType, toolName,
  toolInput, toolOutput, userPrompt,
  imageData, modality, ...
}
  ↓
Step 1: Extract strings for narrative
  - toolName = raw.toolName ?? raw.hookType  // e.g., "read"
  - inputStr = stringifyForNarrative(raw.toolInput)   // Args summary
  - outputStr = stringifyForNarrative(raw.toolOutput) // Output summary
  - promptStr = raw.userPrompt ?? ""
  ↓
Step 2: Infer observation type
  - inferType(toolName, hookType)
  - Map: "bash" → "command_run", "read" → "file_read", "edit" → "file_edit", etc.
  - Default: "other"
  ↓
Step 3: Extract file paths
  - extractFiles(raw.toolInput)
  - Regex: /['/"]([a-zA-Z0-9._/-]+\.(?:ts|js|py|rs|go|java|cpp))['/"]?/g
  - Example: "/src/middleware/auth.ts"
  ↓
Step 4: Build CompressedObservation
  - CompressedObservation = {
      id: obsId,
      sessionId,
      timestamp,
      type: inferredType,                // "file_read"
      title: truncate(toolName, 80),     // "read"
      subtitle: truncate(inputStr, 120), // "src/middleware/auth.ts"
      facts: [],                          // Empty (synthetic has no extraction)
      narrative: truncate(              // Concatenate and truncate to 400 chars
        [promptStr, inputStr, outputStr].filter(s => s.length > 0).join(" | "),
        400
      ),
      // Example narrative: "Check auth middleware | src/middleware/auth.ts | export function validateToken..."
      concepts: [],                       // Empty (synthetic has no NLP)
      files: extractedFiles,              // ["src/middleware/auth.ts"]
      importance: 5,                      // Default neutral
      confidence: 0.3,                    // Low confidence (no LLM)
      imageData: raw.imageData,
      modality: raw.modality
    }
```

### Phase 2X: Index into Search (Both paths)

```typescript
CompressedObservation (from Phase 2A or 2B)
  ↓
Step 1: Add to BM25 index
  - getSearchIndex().add(CompressedObservation)
    - Tokenize: title + narrative + concepts
    - BM25 scoring: term frequency, inverse document frequency
    - Inverted index: term → [docIds]
  ↓
Step 2: Embed narrative + concepts for vector search
  - text = CompressedObservation.title + " " + (narrative || "")
  - CALL embedding provider:
    - embedBatch([text]) if batch available
    - Else: embed(text) → Float32Array of dim 384|768|1536|3072
  ↓
Step 3: Add to vector index
  - vectorIndexAddGuarded(obsId, sessionId, text, metadata)
    - In-memory store: obsId → (embedding: float32[], metadata)
    - Persist to SQLite (for restart recovery)
  ↓
Step 4: Store CompressedObservation in KV (overwrite raw)
  - await kv.set(KV.observations(sessionId), obsId, CompressedObservation)
  - Location: `observations:{project}[obsId]`
  - Value size: 2–5 KB
  ↓
Step 5: Broadcast compressed to viewers (real-time streams)
  - Trigger stream::set
    - data: { type: "compressed", observation: CompressedObservation }
  - Trigger stream::send (to viewers)
    - data: { type: "compressed_observation", observation, sessionId }
```

**KV State after Phase 2:**
```
observations:{project}[obsId] = CompressedObservation {
  id: "obs-xyz789",
  sessionId: "sess-abc123",
  type: "file_read",
  title: "Read auth.ts",
  facts: ["Validates JWT", "Uses jose"],
  narrative: "Checked authentication middleware implementation",
  concepts: ["authentication", "jwt"],
  files: ["src/middleware/auth.ts"],
  importance: 7,
  confidence: 0.85
}

bm25:index:{project} = {
  // Inverted index updated
  "auth" → ["obs-xyz789", "obs-xyz790", ...]
  "jwt" → ["obs-xyz789", ...]
  "middleware" → ["obs-xyz789", ...]
}

vector:index:{project} = {
  // Vector store updated
  "obs-xyz789" → float32[384] (embedding of narrative)
}
```

### Phase 3: Consolidation (SessionEnd Hook, Async)

**Trigger:** SessionEnd hook fires (or manual `POST /agentmemory/consolidate`)

**Function:** `mem::consolidation-pipeline` (async, ~5–30 seconds)

```typescript
Input: {
  sessionId: "sess-abc123",
  force: false
}
  ↓
Step 1: Fetch all observations from session
  - await kv.list(KV.observations(sessionId))
  - Fetch 50+ CompressedObservations from this session
  ↓
Step 2: Summarize session (LLM)
  - Input: observations[0..N].narrative + title
  - Prompt: "Summarize this development session in 2–3 sentences. Include key decisions."
  - LLM response: Generate SessionSummary
    - title: "Added JWT authentication to API"
    - narrative: "Implemented token validation middleware using jose library for Edge compatibility. Added comprehensive tests and rate limiting."
    - keyDecisions: ["Use jose over jsonwebtoken for Edge compat", "1-hour token expiry"]
    - filesModified: ["/src/middleware/auth.ts", "/test/auth.test.ts", ...]
    - concepts: ["authentication", "jwt", "security"]
  ↓
Step 3: Extract entities from observations
  - Regex patterns:
    - File paths: /[a-zA-Z0-9._/-]+\.(ts|js|py|rs)/ → ["src/middleware/auth.ts", ...]
    - Functions: /(?:function|const|export) ([a-zA-Z0-9_]+)/ → ["validateToken", ...]
    - Concepts: From all concepts[] → deduplicate
  ↓
Step 4: Build knowledge graph edges
  - Extract relationships:
    - validateToken DEPENDS_ON jose
    - auth.ts IMPORTS jwt
    - API.ts USES auth middleware
  - Create edges:
    - {source: "validateToken", target: "jose", type: "depends_on", weight: 0.8}
    - {source: "auth.ts", target: "jwt", type: "imports", weight: 0.9}
  - Store in KV:
    - graph:entities:{project}["validateToken"] = [{relType: "depends_on", target: "jose"}]
    - graph:edges:{project}["validateToken→jose"] = {type: "depends_on", weight: 0.8}
  ↓
Step 5: Semantic clustering (LLM)
  - Cluster observations into semantic groups:
    - Group 1: "Authentication implementation" (5 observations)
    - Group 2: "Testing & validation" (3 observations)
    - Group 3: "Deployment & docs" (2 observations)
  ↓
Step 6: Extract reusable facts (semantic tier)
  - For each cluster, identify patterns:
    - "Use jose middleware for JWT validation"
    - "Set token expiry to 1 hour"
    - "Test token validation with expired tokens"
  - Create Memory objects:
    ↓
Memory #1 = {
  id: "mem-abc123",
  createdAt: "2026-05-26T13:00:00Z",
  type: "architecture",                  // vs "pattern", "bug", "workflow"
  title: "Use jose middleware for JWT validation",
  content: "We use jose library instead of jsonwebtoken for Edge compatibility. Tokens expire after 1 hour.",
  concepts: ["authentication", "jwt", "edge", "security"],
  files: ["src/middleware/auth.ts"],
  sessionIds: ["sess-abc123"],
  version: 1,
  strength: 1.0,                         // Will decay over time
  isLatest: true,
  sourceObservationIds: ["obs-xyz789", "obs-xyz790"]
}

Memory #2 = {
  id: "mem-def456",
  type: "workflow",
  title: "JWT token validation test pattern",
  content: "Test expired tokens, invalid signatures, missing headers...",
  ...
}
  ↓
Step 7: Store SessionSummary
  - await kv.set(KV.sessionSummary(project), sessionId, SessionSummary)
  - Location: `sessions:summary:{project}[sessionId]`
  ↓
Step 8: Store Memory objects
  - await kv.set(KV.memories(project), memId, Memory)
  - Location: `memories:{project}[memId]`
  ↓
Step 9: Add memories to BM25 + vector index
  - Same as Phase 2X (step 1–3)
  - Memories are indexed alongside observations
  ↓
Step 10: Update Session
  - session.status = "completed"
  - session.summary = SessionSummary.narrative
  - session.endedAt = now()
  - await kv.update(KV.sessions, sessionId, updates)
```

**KV State after Phase 3:**
```
sessions:summary:{project}[sess-abc123] = SessionSummary {
  sessionId: "sess-abc123",
  project: "myproject",
  createdAt: "2026-05-26T13:00:00Z",
  title: "Added JWT authentication to API",
  narrative: "Implemented token validation middleware...",
  keyDecisions: ["Use jose...", "1-hour expiry"],
  filesModified: ["src/middleware/auth.ts", ...],
  concepts: ["authentication", "jwt"],
  observationCount: 42
}

memories:{project}[mem-abc123] = Memory {
  id: "mem-abc123",
  createdAt: "2026-05-26T13:00:00Z",
  type: "architecture",
  title: "Use jose middleware for JWT validation",
  content: "...",
  strength: 1.0,
  sessionIds: ["sess-abc123"],
  sourceObservationIds: ["obs-xyz789", "obs-xyz790"]
}

graph:entities:{project}["validateToken"] = [
  {relType: "depends_on", target: "jose"},
  {relType: "used_in", target: "auth.ts"}
]

graph:edges:{project}["validateToken→jose"] = {
  type: "depends_on",
  weight: 0.8,
  timestamp: "2026-05-26T13:00:00Z"
}
```

### Phase 4: Decay & Auto-Forget (Nightly, optional)

**Function:** `mem::auto-forget` (triggered by cron or manual)

```typescript
Input: {
  project: "myproject"
}
  ↓
Step 1: Apply Ebbinghaus decay curve
  - For each Memory:
    - daysOld = (now - createdAt) / 86400000
    - strength *= 0.95 ^ daysOld  // Exponential decay
    - If strength < 0.1: evict
  ↓
Step 2: Apply TTL eviction
  - For each Memory with forgetAfter set:
    - If now > forgetAfter: delete Memory
  ↓
Step 3: Update Memory strength based on retrieval
  - Memories recently retrieved via search:
    - strength += 0.5 (reinforcement)
    - isLatest = true (active memory)
  ↓
Step 4: Remove from indexes
  - BM25: remove tokens for evicted memories
  - Vector: remove embeddings
  ↓
Result: Old, unused memories fade away automatically
```

---

### Summary: Data Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 1A: HOOK ARRIVES                                          │
├─────────────────────────────────────────────────────────────────┤
│ HookPayload                                                     │
│ {hookType, sessionId, project, data: {...}}                    │
│              ↓                                                   │
│ POST /agentmemory/observe → mem::observe function              │
└─────────────────────────────────────────────────────────────────┘
                             ↓
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 1B: VALIDATION & DEDUP                                    │
├─────────────────────────────────────────────────────────────────┤
│ • Validate sessionId, hookType, timestamp                      │
│ • Generate obsId                                                │
│ • Dedup check (SHA-256 hash of toolName + input)              │
│ • Privacy filter (strip API keys, <private> tags)             │
│ • Image extraction (detect data:image/...)                     │
└─────────────────────────────────────────────────────────────────┘
                             ↓
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 1C: STORE RAW OBSERVATION                                 │
├─────────────────────────────────────────────────────────────────┤
│ KV SET: observations:{project}[obsId] = RawObservation         │
│ • Store raw data before any LLM processing                     │
│ • Stream to viewers (real-time)                                │
│ • Update session.observationCount                              │
└─────────────────────────────────────────────────────────────────┘
                             ↓
      ┌──────────────────────┴──────────────────────┐
      ↓                                              ↓
┌──────────────────────┐           ┌──────────────────────┐
│ PHASE 2A: LLM        │           │ PHASE 2B: SYNTHETIC  │
│ COMPRESSION          │           │ COMPRESSION          │
├──────────────────────┤           ├──────────────────────┤
│ IF AGENTMEMORY_AUTO_ │           │ DEFAULT PATH         │
│ COMPRESS=true        │           │ (no LLM cost)        │
│ • Call Claude        │           │ • BM25 tokenization  │
│ • Extract facts,     │           │ • File path regex    │
│   concepts, etc.     │           │ • Build narrative    │
│ • Parse XML response │           │   from tool output   │
│ • Quality score:0.85 │           │ • Quality score:0.3  │
└──────────────────────┘           └──────────────────────┘
      ↓                                      ↓
      └──────────────────────┬───────────────┘
                             ↓
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 2X: INDEX INTO SEARCH                                     │
├─────────────────────────────────────────────────────────────────┤
│ • BM25 index: term → [docIds]                                  │
│ • Vector index: embed(narrative + concepts) → float32[]        │
│ KV SET: observations:{project}[obsId] = CompressedObservation │
│ Stream to viewers                                               │
└─────────────────────────────────────────────────────────────────┘
                             ↓
         (Session continues, observations accumulate...)
                             ↓
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 3: CONSOLIDATION (SessionEnd hook)                        │
├─────────────────────────────────────────────────────────────────┤
│ • List all observations in session (50+)                       │
│ • LLM summarize: observations → SessionSummary                 │
│ • Entity extraction: files, functions, patterns                │
│ • Build knowledge graph edges                                  │
│ • Semantic clustering: observations → concepts                 │
│ • Extract reusable facts → Memory objects                      │
│                                                                 │
│ KV SET: sessions:summary:{project}[sessionId] = SessionSummary│
│ KV SET: memories:{project}[memId] = Memory (1..N)             │
│ KV SET: graph:entities:{project}[entity] = relationships      │
│ KV SET: graph:edges:{project}[source→target] = relationship   │
│                                                                 │
│ • Add memories to BM25 + vector index                         │
│ • Update session.status = "completed"                         │
└─────────────────────────────────────────────────────────────────┘
                             ↓
         (Days pass, memory ages...)
                             ↓
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 4: DECAY & AUTO-FORGET (Nightly)                         │
├─────────────────────────────────────────────────────────────────┤
│ • Memory.strength *= 0.95^daysOld (Ebbinghaus)                 │
│ • If strength < 0.1 OR forgetAfter passed: delete              │
│ • Recently retrieved memories: strength += 0.5 (reinforce)     │
│ • Remove from BM25 + vector indexes                            │
│                                                                 │
│ KV DELETE: memories:{project}[memId]                           │
│ Update indexes                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

---

## Tool Calls & Function Registry

### A. iii Functions (123 registered)

**Format:** `sdk.registerFunction(functionId, handler)`
- Accessible via:
  - WebSocket client (iii SDK: Python, Rust, Node)
  - REST proxy (`:3111/agentmemory/*`)
  - MCP tools (53 tools)

#### **Core Memory Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::observe` | `{sessionId, hookType, toolName?, raw}` | `{observationId}` | Capture raw observation from hook |
| `mem::compress` | `{obsId, content}` | `{facts, narrative, concepts, importance}` | Extract structured facts (LLM or synthetic) |
| `mem::search` | `{project, query, limit?}` | `[{obsId, relevance, snippet}]` | BM25 keyword search |
| `mem::smart-search` | `{project, query, limit?, mode?}` | `[{memory or obs}]` | Hybrid search (BM25 + vector + graph) |
| `mem::recall` | `{project, query, limit?}` | `[memories]` | Synonym for smart-search + context formatting |
| `mem::context` | `{project, sessionId?}` | `{context: string, tokens: int}` | Generate injection context for SessionStart |
| `mem::remember` | `{project, type, title, content, concepts, files}` | `{memoryId}` | Manually save long-term memory |
| `mem::forget` | `{project, memoryIds?: [], sessionIds?: []}` | `{deleted: int}` | Delete observations/memories with audit |
| `mem::file-history` | `{project, filePath, limit?}` | `[{obsId, action, timestamp}]` | Past observations for a file |
| `mem::profile` | `{project}` | `{topConcepts[], topFiles[], topPatterns[]}` | Project intelligence snapshot |
| `mem::export` | `{project}` | `{sessions, observations, memories, graph}` | Export all data as JSON |
| `mem::import` | `{project, data: {sessions[], observations[]}}` | `{imported: int}` | Import JSON backup |

#### **Advanced Retrieval Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::timeline` | `{project, sessionId?, limit?}` | `[{timestamp, obs}]` | Chronological observations (sliding window) |
| `mem::relations` | `{project, obsId}` | `{relatedIds[], graph}` | Query relationship graph |
| `mem::graph-query` | `{project, entities[], mode}` | `[{entity, neighbors}]` | Knowledge graph BFS traversal |
| `mem::patterns` | `{project, limit?}` | `[{pattern, count, examples[]}]` | Recurring patterns detected |
| `mem::consolidate` | `{project, sessionId?}` | `{consolidated: int, summaryId}` | Trigger 4-tier consolidation |

#### **Consolidation & Lifecycle Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::consolidation-pipeline` | `{sessionId, force?}` | `{success, sessionSummary}` | Full 4-tier consolidation (SessionEnd hook) |
| `mem::auto-forget` | `{project}` | `{evicted: int}` | Apply Ebbinghaus decay + TTL eviction |
| `mem::claude-bridge-sync` | `{project}` | `{synced: int}` | Bi-directional sync with MEMORY.md |
| `mem::compress-file` | `{filePath, content}` | `{compressed}` | Markdown file compression while preserving structure |

#### **Team & Collaboration Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::team-share` | `{memoryIds[], teamId, mode: "read"|"edit"}` | `{shared: int}` | Share memories with team members |
| `mem::team-feed` | `{teamId, limit?}` | `[{memory, sharedBy, sharedAt}]` | Recent shared items (last 24h) |
| `mem::audit` | `{project?, action?, limit?}` | `[{timestamp, action, userId, target}]` | Audit trail (all mutations logged) |
| `mem::governance-delete` | `{memoryIds[], reason}` | `{deleted: int, auditId}` | Compliance-safe deletion |

#### **Orchestration Functions (Actions, Leases, Routines)**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::action-create` | `{actionId, title, dependencies?: [], dueAt?}` | `{actionId}` | Create DAG work item |
| `mem::action-update` | `{actionId, status, result?}` | `{success}` | Update action progress |
| `mem::frontier` | `{project}` | `[{actionId, priority, blockedBy}]` | Unblocked actions ranked by priority |
| `mem::next` | `{project}` | `{actionId, title, context}` | Single most important next action |
| `mem::lease-acquire` | `{actionId, agentId, durationMs}` | `{leaseId, expiresAt}` | Exclusive multi-agent lock |
| `mem::lease-renew` | `{leaseId, durationMs}` | `{expiresAt}` | Extend lease lifetime |
| `mem::routine-run` | `{routineId, params}` | `{executionId}` | Instantiate workflow routine |
| `mem::signal-send` | `{targetAgentId, message, metadata}` | `{signalId}` | Inter-agent message |
| `mem::signal-read` | `{agentId, limit?}` | `[{signalId, from, message, receivedAt}]` | Inbox + receipt tracking |
| `mem::checkpoint` | `{checkpointId, condition}` | `{ready: boolean}` | External condition gate |

#### **Sketches & Crystallization Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::sketch-create` | `{sketchId, actions: []}` | `{sketchId}` | Ephemeral action graph (in-memory) |
| `mem::sketch-promote` | `{sketchId}` | `{promotedTo: routineId}` | Promote sketch to permanent routine |
| `mem::crystallize` | `{actionIds[]}` | `{crystallizedId}` | Compact action chains into single action |

#### **Mesh & Sentinel Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::mesh-sync` | `{peer: {host, port}, mode: "push"|"pull"}` | `{synced: int}` | P2P memory replication between instances |
| `mem::sentinel-create` | `{sentinelId, event, handler}` | `{sentinelId}` | Event-driven watcher |
| `mem::sentinel-trigger` | `{sentinelId}` | `{triggered: int}` | Manually fire sentinel externally |

#### **Health & Diagnostics Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::diagnose` | `{project?, verbose?}` | `{health, issues, recommendations}` | System health checks |
| `mem::heal` | `{issue}` | `{resolved}` | Auto-fix stuck state |

#### **Facets & Advanced Query Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::facet-tag` | `{memoryId, dimension, value}` | `{tagged}` | Add dimension:value tags for filtering |
| `mem::facet-query` | `{project, facets: {dim: [val]}}` | `[{memory}]` | Filter memories by facet tags |

#### **Vision & Image Search Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `vision::embed-image` | `{imageData, format}` | `{imageId, embedding}` | Embed image for multimodal search |
| `vision::search-images` | `{project, query, limit?}` | `[{imageId, relevance, caption}]` | Search memories by image content |

#### **Replay & Timeline Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::replay-sessions` | `{project?, limit?}` | `[{sessionId, startedAt, observations}]` | List replayable sessions |
| `mem::replay-load` | `{sessionId}` | `{events: [...]}` | Load session timeline for playback |
| `mem::replay-import-jsonl` | `{filePath}` | `{imported: int}` | Import Claude Code JSONL transcripts |

#### **Slot Management Functions** (optional, `AGENTMEMORY_SLOTS=true`)

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::slot-set` | `{slotName, content}` | `{updated}` | Update pinned memory slot (persona, preferences, guidance, etc.) |
| `mem::slot-get` | `{slotName}` | `{content}` | Retrieve pinned slot |
| `mem::slot-reflect` | `{sessionId}` | `{updated: int}` | Auto-append TODOs to pending_items slot (SessionEnd) |

#### **Working Memory Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::working-push` | `{context}` | `{workingId}` | Push temporary context (session-scoped) |
| `mem::working-pop` | `{count?}` | `[{context}]` | Pop working memory stack |
| `mem::working-clear` | `{}` | `{cleared}` | Clear entire working stack |

#### **Query Expansion & Temporal Graph Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::query-expand` | `{query}` | `{expanded: [terms]}` | Synonym expansion + semantic synonyms |
| `mem::temporal-graph` | `{project, timeRange}` | `{graph with timestamps}` | Time-aware relationship graph |
| `mem::retention-score` | `{memoryId}` | `{score, decayRate}` | Predict eviction likelihood |

#### **Lesson Learning Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::lessons-extract` | `{sessionId}` | `[{lesson, context, timestamp}]` | Extract lessons from session |
| `mem::lessons-query` | `{project, topic}` | `[{lesson, strength}]` | Retrieve lessons by topic |

#### **Skill Extraction & Fine-tuning Functions**

| Function ID | Input | Output | Description |
|---|---|---|---|
| `mem::skill-extract` | `{sessionId}` | `[{skill, level, examples}]` | Detect acquired skills from session |
| `mem::skill-search` | `{project, skillName}` | `[{memory, demonstrating}]` | Find memories demonstrating a skill |

---

### B. REST API Endpoints (124)

**Base:** `http://localhost:3111/agentmemory`

#### **Health & Status**

| Endpoint | Method | Auth | Returns |
|---|---|---|---|
| `/health` | `GET` | ✗ | `{status, service, version}` |
| `/status` | `GET` | ✓ | `{sessions, observations, memories, uptime}` |

#### **Session Management**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/session/start` | `POST` | ✗ | `{project, cwd}` | `{sessionId, context: string}` |
| `/session/end` | `POST` | ✗ | `{sessionId}` | `{sessionId, summary}` |
| `/sessions` | `GET` | ✓ | `project, limit, offset` | `[{sessionId, startedAt, status}]` |
| `/session/:id` | `GET` | ✓ | — | `{session details}` |

#### **Observation Management**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/observe` | `POST` | ✗ | `{sessionId, hookType, raw}` | `{observationId, compressed}` |
| `/observations` | `GET` | ✓ | `project, sessionId?, limit` | `[{obsId, type, title}]` |
| `/observation/:id` | `GET` | ✓ | — | `{raw + compressed}` |

#### **Memory Search**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/search` | `POST` | ✗ | `{project, query, limit?}` | `[{memoryId, relevance, snippet}]` |
| `/smart-search` | `POST` | ✗ | `{project, query, limit?, mode?}` | `[{memory or obs, rank}]` |
| `/recall` | `POST` | ✗ | `{project, query, limit?}` | `[{memory, context}]` (formatted) |
| `/file-history` | `GET` | ✓ | `project, filePath` | `[{obsId, action, timestamp}]` |

#### **Memory Management**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/remember` | `POST` | ✗ | `{project, type, title, content, ...}` | `{memoryId}` |
| `/memories` | `GET` | ✓ | `project, limit, type?` | `[{memoryId, title, type}]` |
| `/memory/:id` | `GET` | ✓ | — | `{memory details, sourceObsIds}` |
| `/memory/:id` | `PATCH` | ✓ | `{title?, content?, concepts?}` | `{updated}` |
| `/forget` | `POST` | ✓ | `{memoryIds?: [], sessionIds?: [], reason}` | `{deleted, auditId}` |

#### **Context & Profile**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/context` | `POST` | ✗ | `{project, sessionId?}` | `{context: string, tokens, sources}` |
| `/profile` | `GET` | ✓ | `project` | `{concepts, files, patterns, topMissingPatterns}` |
| `/enrich` | `POST` | ✓ | `{project, filePath, lineStart?, lineEnd?}` | `{cleanup, memories, bugs}` |

#### **Graph & Relations**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/graph/query` | `POST` | ✓ | `{project, entities[], mode}` | `{entities, edges, traversal}` |
| `/relations` | `POST` | ✓ | `{project, obsId}` | `{relatedIds, strength}` |
| `/graph/stats` | `GET` | ✓ | `project` | `{nodes, edges, density}` |

#### **Consolidation & Lifecycle**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/consolidate` | `POST` | ✓ | `{project, sessionId?, force?}` | `{consolidated, summaryId}` |
| `/timeline` | `GET` | ✓ | `project, sessionId?` | `[{timestamp, obs}]` |
| `/lessons` | `GET` | ✓ | `project, topic?` | `[{lesson, strength}]` |

#### **Team & Governance**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/team/share` | `POST` | ✓ | `{memoryIds[], teamId, mode}` | `{shared}` |
| `/team/feed` | `GET` | ✓ | `teamId, limit?` | `[{memory, sharedBy}]` |
| `/audit` | `GET` | ✓ | `project?, action?, limit?` | `[{timestamp, action, target}]` |
| `/governance/delete` | `POST` | ✓ | `{memoryIds[], reason}` | `{deleted, auditId}` |

#### **Orchestration Endpoints**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/actions` | `POST` | ✓ | `{actionId, title, dependencies?}` | `{actionId}` |
| `/frontier` | `GET` | ✓ | `project` | `[{actionId, priority}]` |
| `/next` | `GET` | ✓ | `project` | `{actionId, title, context}` |
| `/leases/:actionId` | `POST` | ✓ | `{agentId, durationMs}` | `{leaseId, expiresAt}` |
| `/routines/:id/run` | `POST` | ✓ | `{params}` | `{executionId, result}` |
| `/signals` | `POST` | ✓ | `{targetAgentId, message}` | `{signalId}` |
| `/signals/inbox` | `GET` | ✓ | `agentId, limit?` | `[{signalId, from, message}]` |

#### **Data Management**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/export` | `GET` | ✓ | `project` | `{sessions, observations, memories, graph}` (JSON) |
| `/import` | `POST` | ✓ | `{project, data}` | `{imported}` |
| `/snapshot/create` | `POST` | ✓ | `{project}` | `{snapshotId, path}` |
| `/snapshot/:id` | `GET` | ✓ | — | `{metadata, diff}` |

#### **Viewer & Diagnostics**

| Endpoint | Method | Auth | Input | Returns |
|---|---|---|---|---|
| `/viewer` | `GET` | ✓ | — | `{HTML document}` |
| `/diagnose` | `GET` | ✓ | `verbose?` | `{health, issues, recommendations}` |
| `/metrics` | `GET` | ✓ | — | `{tokenBudget, usage, providers}` |

---

### C. MCP Tools (53)

**Available when:**
- Running `npx @agentmemory/agentmemory mcp` (stdio MCP server)
- Configured in agent's MCP config (proxies to `:3111` when `AGENTMEMORY_URL` set)
- Falls back to 7 core tools if no server is reachable

#### **Core Tools (8 — always available)**

1. **`memory_recall`** – Search past observations
2. **`memory_compress_file`** – Compress markdown files
3. **`memory_save`** – Save an insight
4. **`memory_patterns`** – Detect recurring patterns
5. **`memory_smart_search`** – Hybrid semantic + keyword search
6. **`memory_file_history`** – Past observations for specific files
7. **`memory_sessions`** – List recent sessions
8. **`memory_profile`** – Project profile (concepts, files, patterns)

#### **Extended Tools (51 total with `AGENTMEMORY_TOOLS=all`)**

**Search & Retrieval (8 tools)**
- `memory_smart_search` – Hybrid BM25 + vector + graph
- `memory_recall` – Semantic search + context formatting
- `memory_file_history` – Chronological file observations
- `memory_timeline` – Observations by timestamp
- `memory_relations` – Relationship graph queries
- `memory_graph_query` – Knowledge graph BFS
- `memory_patterns` – Recurring pattern detection
- `memory_profile` – Project intelligence snapshot

**Memory Management (6 tools)**
- `memory_save` – Manually save long-term memory
- `memory_forget` – Delete observations/memories (with audit)
- `memory_governance_delete` – Compliance-safe deletion
- `memory_consolidate` – Trigger consolidation
- `memory_claude_bridge_sync` – Sync MEMORY.md
- `memory_export` – Export all data as JSON

**Team & Collaboration (3 tools)**
- `memory_team_share` – Share with team members
- `memory_team_feed` – Recent shared items
- `memory_audit` – Audit trail

**Orchestration (12 tools)**
- `memory_action_create` – Create DAG work item
- `memory_action_update` – Update action status
- `memory_frontier` – Unblocked actions
- `memory_next` – Single most important action
- `memory_lease` – Exclusive multi-agent lock
- `memory_routine_run` – Instantiate workflow
- `memory_signal_send` – Inter-agent messaging
- `memory_signal_read` – Read inbox with receipts
- `memory_checkpoint` – External condition gate
- `memory_sketch_create` – Ephemeral action graph
- `memory_sketch_promote` – Promote sketch to routine
- `memory_crystallize` – Compact action chains

**Advanced Features (16 tools)**
- `memory_flow_compress` – Compact consecutive operations
- `memory_mesh_sync` – P2P memory replication
- `memory_sentinel_create` – Event-driven watchers
- `memory_sentinel_trigger` – Manually fire sentinels
- `memory_branch_aware` – Git branch-aware queries
- `memory_verify` – Trace memory provenance
- `memory_working_push/pop/clear` – Working memory stack
- `memory_facet_tag` – Dimension:value tagging
- `memory_facet_query` – Filter by facet tags
- `memory_heal` – Auto-fix stuck state
- `memory_diagnose` – System health checks
- `memory_lessons_extract` – Extract lessons
- `memory_obsidian_export` – Export to Obsidian vault
- `vision_embed_image` – Multimodal image embedding
- `vision_search_images` – Image-based search

#### **MCP Resources (6)**

1. **`agentmemory://status`** – Health, session count, memory count
2. **`agentmemory://project/{name}/profile`** – Per-project intelligence
3. **`agentmemory://memories/latest`** – Latest 10 active memories
4. **`agentmemory://memories/trending`** – Most recently accessed
5. **`agentmemory://graph/stats`** – Knowledge graph statistics
6. **`agentmemory://sessions/active`** – Live sessions

#### **MCP Prompts (3)**

1. **`recall_context`** – Search + return context messages
2. **`session_handoff`** – Handoff data between agents
3. **`detect_patterns`** – Analyze recurring patterns

#### **MCP Skills (4 slash commands)**

1. **`/recall <query>`** – Search memory
2. **`/remember`** – Save to long-term memory
3. **`/session-history`** – Recent session summaries
4. **`/forget <ids>`** – Delete observations

---

## Detailed Schema Definitions

### Core Entity Schemas

#### **Session**
```typescript
{
  id: string;                    // "sess-abc123"
  project: string;               // "myproject"
  cwd: string;                   // "/home/user/project"
  startedAt: string;             // ISO 8601 timestamp
  endedAt?: string;              // ISO 8601 timestamp (optional, set on SessionEnd)
  status: "active" | "completed" | "abandoned";
  observationCount: number;      // Incremented per observation
  model?: string;                // e.g., "claude-opus-4"
  tags?: string[];               // ["auth-flow", "bug-fix"]
  firstPrompt?: string;          // First 200 chars of first user prompt
  summary?: string;              // Set by consolidation pipeline
  commitShas?: string[];         // Git commits linked to session
  updatedAt?: string;            // Last observation timestamp
}
```

**KV Location:** `sessions:{project}[sessionId]`

#### **RawObservation**
Stored immediately upon capture (before compression).
```typescript
{
  id: string;                    // "obs-xyz789"
  sessionId: string;             // References session.id
  timestamp: string;             // ISO 8601 (from hook)
  hookType: HookType;            // "post_tool_use" | "post_tool_failure" | "prompt_submit" | ...
  
  // Tool-specific fields (when hookType = post_tool_use/post_tool_failure)
  toolName?: string;             // e.g., "bash", "read", "edit"
  toolInput?: unknown;           // Original input to tool (object or string)
  toolOutput?: unknown;          // Tool stdout/result or error message
  
  // Prompt-specific fields (when hookType = prompt_submit)
  userPrompt?: string;           // User's actual prompt text
  
  // LLM Response fields (when hookType = conversation)
  assistantResponse?: string;    // Claude's full response
  
  // Raw hook payload (includes above fields + more)
  raw: unknown;                  // Original HookPayload.data (privacy-filtered)
  
  // Multimodal
  modality?: "text" | "image" | "mixed";
  imageData?: string;            // File path or base64 data:image/...
}
```

**KV Location:** `observations:{project}[obsId]`  
**TTL:** 24 hours (working tier, can be evicted)  
**Index:** Streamed to viewer real-time via `stream::set` and `stream::send`

#### **CompressedObservation**
Derived from RawObservation via LLM or synthetic compression.
```typescript
{
  id: string;                    // Same as source RawObservation.id
  sessionId: string;             // References session.id
  timestamp: string;             // Same as source
  
  // Observation classification
  type: ObservationType;         // "file_read" | "file_write" | "file_edit" |
                                 // "command_run" | "search" | "web_fetch" |
                                 // "conversation" | "error" | "decision" |
                                 // "discovery" | "subagent" | "notification" |
                                 // "task" | "image" | "other"
  
  // Human-readable extraction
  title: string;                 // Tool name or "observation", ~80 chars max
  subtitle?: string;             // Input args summary, ~120 chars max
  facts: string[];               // Extractive bullet points from output
                                 // Example: ["Set JWT expiry to 1 hour", "Added validation"]
  
  narrative: string;             // Concise summary, ~400 chars max
                                 // Example: "Implemented rate limiting middleware"
  
  concepts: string[];            // Semantic tags extracted by LLM
                                 // Example: ["authentication", "security", "middleware"]
  
  files: string[];               // File paths touched in this observation
                                 // Example: ["/src/middleware/auth.ts", "/test/auth.test.ts"]
  
  // Quality metrics
  importance: number;            // 1–10 scale (default: 5)
                                 // Used for eviction prioritization
  confidence?: number;           // 0–1 (compression quality)
                                 // 1.0 = LLM-extracted, 0.3 = synthetic BM25
  
  // Multimodal
  imageRef?: string;             // File path to stored image
  imageData?: string;            // Base64 or path (from raw)
  imageDescription?: string;     // LLM vision description
  modality?: "text" | "image" | "mixed";
}
```

**KV Location:** `observations:{project}[obsId]` (overwrites raw)  
**TTL:** 7 days (episodic tier)  
**Index:** Added to BM25 index (via `getSearchIndex().add()`) and vector index  
**Derived From:** RawObservation via either:
  - **LLM compression** (if `AGENTMEMORY_AUTO_COMPRESS=true`) — XML extraction via Claude
  - **Synthetic compression** (default) — BM25 tokenization + file extraction

#### **Memory**
Long-term fact consolidated from CompressedObservations (created during SessionEnd consolidation).
```typescript
{
  id: string;                    // "mem-abc123"
  createdAt: string;             // ISO 8601 (consolidation time)
  updatedAt: string;             // Last time strength/content modified
  
  // Classification
  type: MemoryType;              // "pattern" | "preference" | "architecture" |
                                 // "bug" | "workflow" | "fact"
  
  // Content
  title: string;                 // e.g., "Use jose middleware for JWT"
  content: string;               // Full memory text (markdown-friendly)
  
  // Semantic indexing
  concepts: string[];            // ["authentication", "edge-compatible"]
  files: string[];               // ["/src/middleware/auth.ts"]
  
  // Lineage & versioning
  sessionIds: string[];          // Sessions that contributed observations
  version: number;               // Incremented on updates
  parentId?: string;             // If this memory is an update of another
  supersedes?: string[];         // Memory IDs this replaces
  relatedIds?: string[];         // Related memory IDs (bidirectional)
  sourceObservationIds?: string[]; // Compressed observations that birthed this
  
  // Lifecycle
  strength: number;              // 0–10 scale (decay via Ebbinghaus curve)
                                 // 1.0 = newly created, fades with time
                                 // Boosted on retrieval (reinforcement learning)
  isLatest: boolean;             // Whether this is current version
  forgetAfter?: string;          // ISO 8601 TTL (auto-delete timestamp)
  
  // Multimodal
  imageRef?: string;             // Supporting image
  imageData?: string;            // Base64
}
```

**KV Location:** `memories:{project}[memoryId]`  
**TTL:** Unlimited (subject to `forgetAfter` TTL and strength decay)  
**Index:** Added to BM25 + vector index (same as CompressedObservation)

#### **SessionSummary**
Created during consolidation (SessionEnd hook).
```typescript
{
  sessionId: string;             // References session.id
  project: string;               // References session.project
  createdAt: string;             // Consolidation timestamp
  
  // Generated by LLM
  title: string;                 // Session title, e.g., "Add JWT auth to API"
  narrative: string;             // 2–3 sentence summary of work
  keyDecisions: string[];        // Extracted decisions made in session
  filesModified: string[];       // All files touched (unique list)
  concepts: string[];            // Key concepts (deduplicated)
  
  // Metadata
  observationCount: number;      // Total observations in session
}
```

**KV Location:** `sessions:summary:{project}[sessionId]`  
**TTL:** Unlimited  
**Usage:** Episodic memory tier (what happened in each session)

#### **HookPayload** (incoming from agent)
```typescript
{
  hookType: HookType;            // "session_start" | "post_tool_use" | ...
  sessionId: string;             // Session ID from agent
  project: string;               // Project name
  cwd: string;                   // Current working directory
  timestamp: string;             // ISO 8601
  data: {
    // For post_tool_use:
    tool_name: string;
    tool_input: unknown;
    tool_output: unknown;
    
    // For prompt_submit:
    prompt: string;
    
    // For conversation:
    assistant_response: string;
    
    // Generic payload (includes above + any extra fields)
    [key: string]: unknown;
  };
}
```

---

## State Schema (KV Scopes)

**34 named KV state scopes** (iii primitives):

### Project-Level Scopes (per project)

| Scope | Key Pattern | Value Type | TTL | Size | Notes |
|---|---|---|---|---|---|
| `sessions:{project}` | `{sessionId}` | `Session` | unlimited | 1–2 KB | Master session list |
| `observations:{project}` | `{obsId}` | `RawObservation` \| `CompressedObservation` | 24h (raw) → 7d (compressed) | 1–10 KB | Raw capture → compression |
| `memories:{project}` | `{memoryId}` | `Memory` | unlimited | 2–5 KB | Long-term facts |
| `sessions:summary:{project}` | `{sessionId}` | `SessionSummary` | unlimited | 1 KB | Episodic consolidation |
| `graph:entities:{project}` | `{entity}` | `[{relType, targetEntity, weight}]` | unlimited | 100 B–1 KB | Knowledge graph nodes |
| `graph:edges:{project}` | `{source}→{target}` | `{type, weight, timestamp, confidence}` | unlimited | 200 B | Knowledge graph edges |
| `bm25:index:{project}` | (indexed document store) | Serialized BM25 trie | unlimited | 10–100 MB | Keyword index (persistent) |
| `vector:index:{project}` | (indexed document store) | `float32[] (dim=384|1536|3072)` | unlimited | 50–500 MB | Dense embeddings |
| `slots:{project}` | `{slotName}` | `MemorySlot` | unlimited | 1–10 KB | Pinned editable slots (optional) |

### Session-Level Scopes (per session)

| Scope | Key Pattern | Value Type | Notes |
|---|---|---|---|
| `session:{sessionId}:metadata` | `meta` | `{startedAt, endedAt, status, observationCount}` | Session lifecycle (updated per obs) |
| `session:{sessionId}:observations` | `{obsId}` | `RawObservation` | Observation list (auto-populated) |

### Team & Collaboration Scopes

| Scope | Key Pattern | Value Type | Notes |
|---|---|---|---|
| `team:{teamId}:shared` | `{memoryId}` | `{sharedBy, mode, sharedAt}` | Shared memory links |
| `team:{teamId}:feed` | `{feedId}` | `{memoryId, sharedBy, timestamp}` | Shared activity feed |

### Orchestration Scopes

| Scope | Key Pattern | Value Type | Notes |
|---|---|---|---|
| `actions:{project}` | `{actionId}` | `{title, status, dependencies, dueAt}` | DAG work items |
| `actions:frontier:{project}` | `{actionId}` | `{priority, blockedBy}` | Unblocked queue |
| `leases:{actionId}` | `{leaseId}` | `{agentId, expiresAt}` | Multi-agent locks |
| `routines:{project}` | `{routineId}` | `{template, params, executions}` | Workflow templates |
| `signals:{agentId}` | `{signalId}` | `{from, message, receivedAt}` | Inbox |
| `signals:{agentId}:receipts` | `{signalId}` | `{readAt}` | Receipt tracking |
| `checkpoints:{project}` | `{checkpointId}` | `{condition, satisfied}` | External gates |
| `sentinels:{project}` | `{sentinelId}` | `{event, handler, triggered}` | Event watchers |

### System Scopes

| Scope | Key Pattern | Value Type | Notes |
|---|---|---|---|
| `meta:projects` | `{projectName}` | `{createdAt, lastUsedAt}` | Project catalog |
| `meta:health` | `status` | `{healthy, issues[]}` | Health monitor |
| `meta:config` | `{configKey}` | JSON | Runtime configuration |
| `audit:log:{project}` | `{timestamp}:{operationId}` | `{action, userId, target, reason}` | Audit trail |
| `metrics:usage` | `{project}` | `{tokensUsed, embeddings, compressions}` | Token/API budgets |

---

## Privacy & Security

### Privacy Filter
- **Regex patterns:** OPENAI_API_KEY, password, token, secret, authorization, bearer
- **Tags:** `<private>content</private>` (entire content stripped)
- **Output:** ANSI codes removed
- **Timing:** Before observation storage

### Authentication
- **REST API:** Bearer token (if `AGENTMEMORY_SECRET` set)
- **MCP:** Shim proxies to server (no client-side auth)
- **iii functions:** Operate over WebSocket (no separate auth)
- **Viewer:** HMAC secret in HTTP bearer + CSP nonce per response

### Authorization
- Per-project namespacing: `observations:{project}`, `memories:{project}`
- Team-private scopes: `team:{teamId}:shared` (read restricted by `mode: "read"|"edit"`)
- Governance audit trail: every delete logged with reason + timestamp

---

## Performance & Scaling

### Dedup Window
- **Mechanism:** SHA-256 hash of (toolName, input, output)
- **Window:** 5-minute rolling buffer per session
- **Cost:** O(1) lookup in `DedupMap` (in-memory)

### Compression Batching
- **Async queue:** `iii-queue` worker (optional)
- **Batch size:** up to 10 observations
- **Timeout:** 30 seconds per batch
- **Retry:** Dead-letter queue on LLM timeout

### Vector Index Persistence
- **Format:** SQLite BLOB (float32 array + metadata)
- **Dimensions:** 384 (local) → 3072 (OpenAI)
- **Query:** Cosine similarity O(n*d) per search
- **Rebuild:** `rebuildIndex()` batches lookups via `iii-queue`

### Hybrid Search (RRF Fusion)
- **BM25:** O(log n) per term (binary search on inverted index)
- **Vector:** O(n*d) cosine sim (all docs)
- **Graph:** O(e) BFS from matched entities (e = edges)
- **Fusion:** Reciprocal Rank Fusion (k=60) + session diversity (max 3/session)
- **Latency:** ~50–200ms typical (depends on corpus size)

### Consolidation Pipeline
- **Trigger:** SessionEnd hook (async, fire-and-forget)
- **Duration:** 5–30 seconds (LLM-dependent)
- **Stages:**
  1. Summarize (30 observations → 1 summary, ~1 token/100 obs)
  2. Extract entities (regex + NLP patterns)
  3. Build graph (O(entities²) relationship scoring)
  4. Cluster (semantic grouping into tiers)
- **Cost:** ~500 tokens/session (LLM provider)

---

## Failure Modes & Resilience

### Circuit Breaker (LLM Provider)
- **Trigger:** 3 consecutive timeouts or 50% error rate over 60s
- **Action:** Fall back to next provider (primary + 2 backups)
- **Recovery:** Exponential backoff (1s → 30s → 300s)
- **Final fallback:** Synthetic compression (zero-LLM)

### Timeout Protection
- **SDK-level:** 180s per function invocation (iii-sdk)
- **LLM-level:** 60s per API call (configurable `AGENTMEMORY_LLM_TIMEOUT_MS`)
- **Embedding-level:** 60s per batch
- **Top-level:** Unhandled rejection handler (suppresses logs every 60s)

### Vector Index Mismatch Detection
- **Trigger:** Active embedding provider dimension ≠ persisted vectors
- **Action:** 
  - Validate all stored vectors on startup
  - If mismatches: refuse startup (unless `AGENTMEMORY_DROP_STALE_INDEX=true`)
  - If enabled: discard stale vectors, rebuild live
- **Cost:** Full index rebuild (hours for 100K+ observations)

### Governance & Audit Trail
- **Every delete:** Logged with timestamp, reason, user ID
- **Every mutation:** Recorded in `audit:log:{project}`
- **Recovery:** Full export/import capability
- **Compliance:** `memory_governance_delete` endpoint for regulated deletion

---

## Configuration Hierarchy

```
Environment Variables (.agentmemory/.env)
├── LLM Providers
│   ├── ANTHROPIC_API_KEY (primary)
│   ├── GEMINI_API_KEY (fallback)
│   ├── OPENAI_API_KEY (fallback)
│   ├── OPENROUTER_API_KEY (fallback)
│   ├── MINIMAX_API_KEY (fallback)
│   └── AGENTMEMORY_ALLOW_AGENT_SDK=true (opt-in, last resort)
├── Embedding Providers
│   ├── EMBEDDING_PROVIDER=local (default, free)
│   ├── GEMINI_API_KEY (100+ languages)
│   ├── OPENAI_API_KEY → text-embedding-3-small
│   ├── VOYAGE_API_KEY → voyage-code-3
│   └── OPENROUTER_API_KEY (multi-model)
├── Search Tuning
│   ├── BM25_WEIGHT=0.4 (default)
│   ├── VECTOR_WEIGHT=0.6 (default)
│   └── AGENTMEMORY_GRAPH_WEIGHT=0.3 (default)
├── Features (flags)
│   ├── AGENTMEMORY_AUTO_COMPRESS=false (off by default, #138)
│   ├── AGENTMEMORY_SLOTS=false (off by default)
│   ├── AGENTMEMORY_REFLECT=false (off, requires SLOTS=true)
│   ├── AGENTMEMORY_INJECT_CONTEXT=false (off by default, #143)
│   ├── GRAPH_EXTRACTION_ENABLED=false (off by default)
│   ├── CONSOLIDATION_ENABLED=true (on by default)
│   ├── LESSON_DECAY_ENABLED=true (on by default)
│   ├── OBSIDIAN_AUTO_EXPORT=false (off by default)
│   ├── AGENTMEMORY_DROP_STALE_INDEX=false (off by default)
│   └── SNAPSHOT_ENABLED=false (off by default)
├── Ports
│   ├── III_REST_PORT=3111 (API + MCP proxy)
│   ├── AGENTMEMORY_VIEWER_PORT=3113 (UI)
│   └── AGENTMEMORY_STREAMS_PORT=49134 (iii WebSocket)
├── Auth
│   ├── AGENTMEMORY_SECRET=<random> (optional bearer token)
│   └── HMAC verification (timing-safe compare)
└── Team/Collab
    ├── TEAM_ID=<id> (namespaced memory)
    ├── USER_ID=<id> (for audit trail)
    └── TEAM_MODE=private|shared
```

---

## Key Design Decisions

### 1. **iii Engine as Foundation**
- **Why:** Eliminates Express, Redis, Postgres, pm2, Prometheus
- **Trade-off:** Tightly coupled to iii versioning (currently v0.11.2)
- **Benefit:** Unified OTEL traces, WebSocket streams, KV state, queues on single primitive

### 2. **BM25 + Vector Fusion (RRF)**
- **Why:** BM25 excels at keyword recall, vector at semantic similarity
- **Trade-off:** O(n*d) vector search; mitigated by Xenova local embeddings
- **Benefit:** 95.2% R@5 on LongMemEval-S; BM25-only fallback if embedding provider fails

### 3. **Synthetic Compression Fallback**
- **Why:** Always index observations, even without LLM provider
- **Trade-off:** BM25 tokenization ≠ semantic extraction (slightly lower quality)
- **Benefit:** Zero API cost, offline capability, no privacy leakage to LLM

### 4. **Lazy Vector Index Rebuild**
- **Why:** Avoid startup delays on large corpora
- **Trade-off:** Live observations build index incrementally
- **Benefit:** 25h → 3h rebuild time (batched embedding jobs via `iii-queue`)

### 5. **SessionEnd Consolidation (Async)**
- **Why:** Non-blocking; keeps viewer + later boot steps responsive
- **Trade-off:** Small race: immediate SessionStart search may miss consolidation
- **Benefit:** Stop hook completes in <100ms; consolidation runs in background

### 6. **4-Tier Memory + Decay**
- **Why:** Mirrors human memory consolidation (Ebbinghaus curve)
- **Trade-off:** Automated eviction; no manual "keep forever" option (workaround: strength boost)
- **Benefit:** Prevents unbounded memory bloat; stale facts auto-fade

### 7. **Knowledge Graph Over RDBMS**
- **Why:** Flexible entity types + relationship semantics
- **Trade-off:** Manual entity extraction (regex + LLM); no ACID guarantees
- **Benefit:** Queryable (RDF-style BFS), integrates with vector search (RRF fusion)

### 8. **Team Namespacing Over RBAC**
- **Why:** Simpler authorization model (read/edit modes)
- **Trade-off:** No row-level granularity; shared ≠ "visible to group"
- **Benefit:** Multi-agent coordination without complex policy evaluation

### 9. **MCP Shim Package**
- **Why:** Works with or without server running (graceful degradation)
- **Trade-off:** 7-tool fallback ≠ full 53-tool surface
- **Benefit:** Agents don't error if agentmemory server crashes; just lose extended tools

### 10. **Governance Delete Over Soft Delete**
- **Why:** Audit-trail compliance; true deletion for GDPR/CCPA
- **Trade-off:** Permanent loss (no undelete)
- **Benefit:** Regulatory alignment; audit trail proves deletion

---

## Extension Points

### Adding a New Tool

```typescript
// src/functions/my-tool.ts
export function registerMyTool(sdk: ISdk, kv: StateKV) {
  sdk.registerFunction("mem::my-tool", async (input) => {
    const result = await kv.get("observations:myproject", "key");
    return { result };
  });
}

// src/index.ts
import { registerMyTool } from "./functions/my-tool.js";
// ...in main()...
registerMyTool(sdk, kv);
```

### Adding a New State Scope

```typescript
// src/state/schema.ts
export type KV = {
  // ...existing...
  "my-scope:{project}": Map<string, MyType>;
};

// Usage:
const myData = await kv.get("my-scope:myproject", "key");
await kv.set("my-scope:myproject", "key", myData);
```

### Adding an Embedding Provider

```typescript
// src/providers/index.ts
if (process.env.MY_EMBEDDING_KEY) {
  return {
    name: "my-provider",
    dimensions: 1536,
    embed: async (text) => {
      const res = await fetch("https://api.my-provider.com/embed", {
        body: JSON.stringify({ text }),
      });
      return (await res.json()).embedding;
    },
  };
}
```

---

## Testing Strategy

### Test Coverage: 950+ unit tests

```
npm test                  # Unit tests (all .test.ts)
npm run test:integration  # API tests (requires running server)
npm run test:eval         # Benchmark suite (LongMemEval-S)
```

### Key Test Suites

- **Privacy filter:** Regex patterns for API keys, secrets, `<private>` tags
- **Dedup:** 5-min SHA-256 window isolation
- **Compression:** LLM vs synthetic extraction quality
- **Search:** BM25 recall, vector cosine, RRF fusion ranking
- **Consolidation:** 4-tier tiering, decay curve correctness
- **Knowledge graph:** Entity extraction, relationship scoring, BFS
- **Governance:** Delete audit trail, GDPR compliance

---

## Observability

### OTEL Traces (iii-observability worker)

Every function invocation emits spans:
- `mem::smart-search` → BM25 scan → embedding lookup → RRF fusion → reranker
- `mem::consolidate` → summarize → extract entities → build graph → cluster
- LLM calls: compression, summarization, entity extraction (with provider name + latency)

### Metrics

- Token usage per session (per project)
- Embedding count + API costs
- Consolidation duration + success rate
- Search latency (p50, p95, p99)
- Vector index size + persistence I/O

### Logs

Structured JSON logs (integration with iii log collector):
- Level: info, warn, error
- Component: privacy, compress, search, consolidate, etc.
- Project, sessionId, operationId for correlation
- Full context (payloads, errors) with privacy filtering

### Health Monitor

```
GET /agentmemory/health
{
  "status": "ok" | "warning" | "critical",
  "uptime": 86400,
  "memory_usage_mb": 256,
  "active_sessions": 12,
  "observations": 15000,
  "memories": 420,
  "issues": [
    {
      "component": "embedding_provider",
      "severity": "warning",
      "message": "1/3 failures in last 60s (fallback active)"
    }
  ]
}
```

---

## Deployment Considerations

### Storage
- **SQLite location:** `~/.agentmemory/data.db` (or `AGENTMEMORY_EXPORT_ROOT`)
- **Disk usage:** ~100MB per 10K observations (depends on compression)
- **Backup:** Export via `GET /agentmemory/export` (full JSON dump)

### Scaling (Multi-Instance)
- **P2P mesh:** `mem::mesh-sync` endpoint for replication
- **Shared state:** Deploy with iii-database worker (SQL-backed KV)
- **Leases:** Multi-agent action coordination via `mem::lease-acquire`
- **Signals:** Inter-instance messaging

### Docker / K8s
- One-click templates: Fly.io, Railway, Render, Coolify
- Dockerfile bundles iii v0.11.2 + Node.js runtime
- Entry-point: Generates `AGENTMEMORY_SECRET`, binds `0.0.0.0`, drops to `node` user
- Viewer `:3113` bound to loopback (SSH tunnel recommended)

---

## Summary

**agentmemory** is a production-grade persistent memory system built on iii primitives, exposing 123 functions across REST, MCP, and WebSocket APIs. The core design — BM25 + vector hybrid search, 4-tier consolidation pipeline, knowledge graph extraction — achieves 95.2% retrieval accuracy at 92% lower token cost than competitors.

**Key strengths:**
- Zero external dependencies (iii + SQLite only)
- 53 MCP tools + 124 REST endpoints
- Real-time viewer + iii console observability
- Team namespacing + governance audit trail
- Extensible via `iii worker add` (pubsub, cron, queue, sandbox, database)

**Trade-offs:**
- Tightly coupled to iii v0.11.2 (migration to sandbox model pending)
- Vector search O(n*d); mitigated by local Xenova embeddings
- Consolidation is async; brief race window before injection
- BM25 synthetic compression < LLM extraction quality

---

**Total Lines of Code:** ~21,800 LOC (118 source files)  
**Functions:** 123 iii functions  
**KV Scopes:** 34 state namespaces  
**Tests:** 950+ unit tests  
**External DBs:** 0 (SQLite only)
