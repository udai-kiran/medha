# agent_mem.md: Hybrid Go + Python Agent Memory System

**Version**: 0.1  
**Date**: 2026-05-26  
**Status**: Architecture & Implementation Plan

---

## Executive Summary

**agent_mem** combines Neo4j Agent Memory (DESIGN.md) and agentmemory (LOW_LEVEL_DESIGN.md) into a production-grade hybrid system:

- **Go service**: Observation capture, state management, hybrid search (BM25 + vector + graph), REST API, MCP server, real-time viewer, consolidation DAG orchestration
- **Python service**: Entity extraction (spaCy → GLiNER → LLM), summarization, enrichment (Wikipedia/Diffbot), relationship extraction
- **Backend**: Neo4j Bolt (graph) + SQLite (KV state + BM25 index)

**Why hybrid?**
- Go excels at: API, async I/O, single binary, goroutines, observability
- Python excels at: NLP extraction, LLM integration, entity typing, ML pipelines
- Combined: Best of both worlds with acceptable IPC overhead (~5% latency)

---

## Architecture Overview

### High-Level Topology

```
┌────────────────────────────────────────────────────────────────┐
│                      Agent Clients                             │
│  (Claude Code, Cursor, Cline, OpenCode, Hermes, etc.)         │
└──────────────┬─────────────────────────────────────────────────┘
               │ POST /agentmemory/observe (HookPayload)
               │ GET /agentmemory/smart-search
               │ POST /agentmemory/remember
               │ WebSocket /agentmemory/stream
               ↓
┌─────────────────────────────────────────────────────────────────┐
│                    Go Service (Port :3111)                      │
│   (api.go + search.go + state.go + consolidation.go)           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ REST API Endpoints (124 routes)                         │   │
│  │  • POST /observe (validation, dedup, privacy filter)    │   │
│  │  • POST /smart-search (BM25 + vector + graph RRF)       │   │
│  │  • GET /search, /recall, /context, /profile            │   │
│  │  • POST /remember, /forget, /consolidate               │   │
│  │  • Orchestration: /actions, /frontier, /leases         │   │
│  │  • Team: /team/share, /team/feed, /audit               │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ MCP Server (53 tools, stdio + HTTP proxy)               │   │
│  │  • Exposes all REST endpoints as MCP tools              │   │
│  │  • 6 MCP resources (status, profile, memories, graph)   │   │
│  │  • 3 MCP prompts + 4 slash skills (/recall, etc.)      │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ State Management                                        │   │
│  │  • SQLite: sessions, observations, memories, graph     │   │
│  │  • KV layer: 34 scopes (project, session, team, etc.)  │   │
│  │  • Indexes: BM25 trie (disk-backed), vector (disk)      │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Search Engine                                           │   │
│  │  • BM25: stemming, synonym expansion, CJK tokenization │   │
│  │  • Vector: cosine sim (float32, dims 384–3072)         │   │
│  │  • Graph: entity matching + RDF BFS traversal           │   │
│  │  • RRF Fusion: reciprocal rank fusion (k=60)            │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Consolidation Orchestrator                              │   │
│  │  • Queue: RabbitMQ/Redis for async jobs                 │   │
│  │  • DAG: actions, dependencies, leases, signals          │   │
│  │  • Triggers: SessionEnd hook → consolidation pipeline   │   │
│  │  • Metrics: prometheus counters + OTEL traces           │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Real-Time Viewer & Observability (:3113)                │   │
│  │  • WebSocket streams (observations, memories, graph)    │   │
│  │  • Dashboard: sessions, memories, search results        │   │
│  │  • Health: component status, error rates, latency (p50) │   │
│  │  • OTEL traces: jaeger/datadog integration              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ Neo4j Integration                                       │   │
│  │  • neo4j-go driver: entity graph, relationships         │   │
│  │  • Vector index: 1536-dim embeddings (for enrichment)   │   │
│  │  • Cypher queries: relationship traversal, analytics    │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────┬──────────────────────┬──────────────────┘
                      │                      │
                      │ gRPC (async jobs)    │ HTTP calls
                      │                      │
        ┌─────────────↓──────────┐  ┌────────↓─────────────────┐
        │  Go Async Job Worker   │  │  Python Service (:5000)  │
        │ (consolidation queue)  │  │   (extraction, LLM)      │
        └───────────────────────┘  └────────┬──────────────────┘
                                            │
                      ┌─────────────────────┴────────────────────┐
                      │                                          │
            ┌─────────↓──────────┐                   ┌───────────↓──────┐
            │  spaCy + GLiNER    │                   │  LLM Providers   │
            │  (local, fast)     │                   │  (Anthropic,     │
            │                    │                   │   OpenAI, etc.)  │
            │  • NER extraction  │                   │                  │
            │  • Rel. extraction │                   │  • Summarize     │
            │  • Dedup check     │                   │  • Compress      │
            │  • Typing (POLE+O) │                   │  • Extract facts │
            └────────────────────┘                   └──────────────────┘
```

---

## Component Breakdown

### Go Service (`agent-mem-go/`)

**Responsibilities:**
- Observation ingestion & validation
- Privacy filtering & deduplication
- State persistence (SQLite)
- Hybrid search orchestration
- REST API & MCP server
- Real-time streaming
- Consolidation DAG
- Neo4j graph operations
- Multi-tenancy & auth
- Observability (OTEL)

**Key Packages:**

```
agent-mem-go/
├── cmd/
│   ├── api/                          # Main HTTP server
│   │   └── main.go                   # Listen :3111 + :3113
│   └── worker/                       # Async consolidation job
│       └── main.go                   # RabbitMQ consumer
│
├── internal/
│   ├── api/
│   │   ├── handlers.go               # HTTP endpoints (124 routes)
│   │   ├── middleware.go             # Auth, logging, CORS
│   │   └── errors.go                 # Error handling
│   │
│   ├── state/
│   │   ├── kv.go                     # KV abstraction (34 scopes)
│   │   ├── sqlite.go                 # SQLite backend
│   │   ├── schema.go                 # Table definitions
│   │   └── migration.go              # Schema versioning
│   │
│   ├── search/
│   │   ├── bm25.go                   # BM25 full-text search
│   │   ├── vector.go                 # Vector similarity (cosine)
│   │   ├── graph.go                  # Knowledge graph BFS
│   │   ├── rrf.go                    # Reciprocal Rank Fusion
│   │   └── hybrid.go                 # Orchestrator (all 3)
│   │
│   ├── privacy/
│   │   ├── filter.go                 # Strip secrets + <private> tags
│   │   ├── regex.go                  # API key patterns
│   │   └── ansi.go                   # Remove ANSI codes
│   │
│   ├── dedup/
│   │   ├── window.go                 # 5-min SHA-256 rolling
│   │   └── store.go                  # In-memory + persistent
│   │
│   ├── consolidation/
│   │   ├── pipeline.go               # 4-tier consolidation DAG
│   │   ├── queue.go                  # RabbitMQ producer
│   │   ├── scheduler.go              # SessionEnd → trigger
│   │   └── decay.go                  # Ebbinghaus eviction
│   │
│   ├── graph/
│   │   ├── neo4j.go                  # Neo4j Bolt driver
│   │   ├── entity.go                 # Entity CRUD (POLE+O)
│   │   ├── relationship.go           # Relationship ops
│   │   └── enrichment.go             # Enrich from Neo4j
│   │
│   ├── mcp/
│   │   ├── server.go                 # MCP stdio server
│   │   ├── tools.go                  # 53 tool definitions
│   │   ├── resources.go              # 6 MCP resources
│   │   ├── prompts.go                # 3 MCP prompts
│   │   └── skills.go                 # 4 slash commands
│   │
│   ├── models/
│   │   ├── observation.go            # RawObservation + CompressedObservation
│   │   ├── memory.go                 # Memory + SessionSummary
│   │   ├── session.go                # Session lifecycle
│   │   ├── action.go                 # Action DAG + Leases
│   │   └── hook.go                   # HookPayload from agents
│   │
│   ├── python/
│   │   ├── client.go                 # Python service HTTP client
│   │   ├── compress.go               # Call py::compress endpoint
│   │   ├── extract.go                # Call py::extract endpoint
│   │   └── summarize.go              # Call py::summarize endpoint
│   │
│   ├── viewer/
│   │   ├── server.go                 # WebSocket :3113
│   │   ├── stream.go                 # Real-time streams
│   │   └── dashboard.go              # HTML/JS frontend
│   │
│   ├── telemetry/
│   │   ├── otel.go                   # OTEL setup (jaeger/datadog)
│   │   ├── metrics.go                # Prometheus counters
│   │   └── logs.go                   # Structured JSON logging
│   │
│   └── config/
│       ├── config.go                 # Env var parsing
│       └── validate.go               # Configuration validation
│
├── go.mod
├── go.sum
└── Dockerfile                        # Single-stage, scratch image
```

**Technology Stack (Go):**
- **Framework**: Chi (lightweight HTTP router)
- **Database**: SQLite (github.com/mattn/go-sqlite3)
- **Graph**: neo4j/neo4j-go-driver/v5
- **MCP**: nlohmann/json (for serialization) or custom JSON
- **Search**: github.com/blevesearch/bleve (BM25 + vector plugin)
- **Queue**: streadway/amqp (RabbitMQ) or redis-go
- **Observability**: go.opentelemetry.io (traces, metrics, logs)
- **WebSocket**: gorilla/websocket
- **HTTP Client**: net/http (stdlib with custom client)

---

### Python Service (`agent-mem-py/`)

**Responsibilities:**
- Entity extraction (spaCy NER → GLiNER → LLM fallback)
- Relationship extraction (GLiREL or LLM)
- Conversation summarization (LLM)
- Observation compression (LLM or synthetic)
- Entity deduplication (fuzzy + semantic matching)
- Entity enrichment (Wikipedia, Diffbot, Wikidata)
- Preference detection (pattern-based)
- Embedding generation (multiple providers)

**Key Modules:**

```
agent-mem-py/
├── agent_mem/
│   ├── __init__.py
│   │
│   ├── api.py                        # FastAPI app (:5000)
│   │   ├── /extract                  # POST → entity extraction
│   │   ├── /compress                 # POST → observation compression
│   │   ├── /summarize                # POST → conversation summary
│   │   ├── /enrich                   # POST → Wikipedia/Diffbot
│   │   ├── /embed                    # POST → vector embeddings
│   │   ├── /health                   # GET → liveness probe
│   │   └── /metrics                  # GET → prometheus
│   │
│   ├── extraction/
│   │   ├── __init__.py
│   │   ├── pipeline.py               # Multi-stage extractor
│   │   ├── spacy_extractor.py        # spaCy NER (fast)
│   │   ├── gliner_extractor.py       # GLiNER (zero-shot)
│   │   ├── llm_extractor.py          # Claude/GPT fallback
│   │   ├── glirer_extractor.py       # Relationship extraction
│   │   ├── entity.py                 # Entity class (name, type, confidence)
│   │   ├── types.py                  # POLE+O enum
│   │   └── merger.py                 # Merge multi-stage results
│   │
│   ├── dedup/
│   │   ├── __init__.py
│   │   ├── fuzzy_matcher.py          # Levenshtein + substring
│   │   ├── semantic_matcher.py       # Embedding-based (cosine sim)
│   │   ├── exact_matcher.py          # Exact string match
│   │   └── resolver.py               # Combine strategies + confidence
│   │
│   ├── enrichment/
│   │   ├── __init__.py
│   │   ├── wikipedia.py              # Wikimedia API
│   │   ├── diffbot.py                # Diffbot knowledge graph
│   │   ├── wikidata.py               # Wikidata IDs + properties
│   │   └── cache.py                  # Local SQLite cache
│   │
│   ├── compression/
│   │   ├── __init__.py
│   │   ├── llm_compressor.py         # Extract facts + narrative
│   │   ├── synthetic_compressor.py   # BM25 tokenization fallback
│   │   └── validator.py              # Quality checks
│   │
│   ├── summarization/
│   │   ├── __init__.py
│   │   ├── conversation.py           # Multi-turn summary
│   │   ├── session.py                # Session summary + decisions
│   │   └── prompt_templates.py       # System prompts
│   │
│   ├── embedding/
│   │   ├── __init__.py
│   │   ├── providers.py              # Provider selection
│   │   ├── openai_embedder.py        # text-embedding-3-small
│   │   ├── gemini_embedder.py        # Google Gemini embeddings
│   │   ├── local_embedder.py         # Xenova all-MiniLM-L6-v2
│   │   ├── voyage_embedder.py        # Voyage AI code embeddings
│   │   └── batch.py                  # Batch embed with batching
│   │
│   ├── preferences/
│   │   ├── __init__.py
│   │   ├── detector.py               # Regex pattern detection
│   │   └── patterns.py               # Preference regex patterns
│   │
│   ├── models/
│   │   ├── __init__.py
│   │   ├── observation.py            # RawObservation schema
│   │   ├── entity.py                 # Entity with extraction metadata
│   │   ├── relationship.py           # Relationship + confidence
│   │   └── compressed.py             # CompressedObservation
│   │
│   ├── providers/
│   │   ├── __init__.py
│   │   ├── llm.py                    # LLM factory (Anthropic, OpenAI, Gemini)
│   │   ├── embedder.py               # Embedding factory
│   │   └── cache.py                  # Response caching
│   │
│   ├── utils/
│   │   ├── __init__.py
│   │   ├── logging.py                # Structured JSON logs
│   │   ├── retry.py                  # Exponential backoff
│   │   └── validators.py             # Input validation
│   │
│   └── config.py                     # Environment + settings
│
├── tests/
│   ├── test_extraction.py
│   ├── test_dedup.py
│   ├── test_enrichment.py
│   ├── test_embedding.py
│   └── conftest.py
│
├── requirements.txt                  # Python dependencies
├── Dockerfile                        # Python 3.11 slim image
└── README.md
```

**Technology Stack (Python):**
- **Framework**: FastAPI (async HTTP server)
- **NLP**: spacy, gliner, spacy-transformers
- **Relationships**: glirer (or LLM fallback)
- **LLMs**: anthropic-sdk, openai, google-generativeai
- **Embeddings**: sentence-transformers, openai, voyage-ai
- **Fuzzy Matching**: rapidfuzz (Levenshtein)
- **Enrichment**: requests (for Wikipedia/Diffbot APIs)
- **Async**: httpx (async HTTP client)
- **Serialization**: pydantic (validation + JSON serialization)
- **Observability**: opentelemetry-api + opentelemetry-instrumentation-fastapi
- **Database**: sqlite3 (local cache for enrichment)

---

## Data Flow

### Phase 1A: Observation Capture (Go Service)

```
Agent fires hook:
  POST /agentmemory/observe
    {
      sessionId: "sess-abc123",
      project: "myproject",
      cwd: "/home/user/project",
      timestamp: "2026-05-26T12:34:56Z",
      hookType: "post_tool_use",
      data: {
        tool_name: "read",
        tool_input: {file_path: "/src/auth.ts"},
        tool_output: "export function validateToken..."
      }
    }
         ↓
Go API handler:
  1. Validate (required fields)
  2. Dedup check: SHA-256(sessionId + toolName + JSON(input))
     → 5-min rolling window per session
     → If seen before: return 202 Accepted (deduplicated)
  3. Privacy filter: strip OPENAI_API_KEY=, password=, <private> tags, ANSI
  4. Image extraction (if data:image/... in raw)
  5. Create RawObservation:
     {
       id: "obs-xyz789",
       sessionId: "sess-abc123",
       timestamp: ...,
       hookType: "post_tool_use",
       toolName: "read",
       toolInput: {...},
       toolOutput: "export function...",  // Sanitized
       raw: {...},  // Privacy-filtered
       modality: "text"
     }
  6. SQLite INSERT: observations table
  7. Update session.observationCount += 1
  8. Broadcast to viewers (WebSocket stream)
  9. Return 201 Created {observationId, compressing: true}
         ↓
Status: RawObservation stored, ready for compression
```

**API Response:**
```json
{
  "status": 201,
  "body": {
    "observationId": "obs-xyz789",
    "compressing": true,
    "compressed": false
  }
}
```

---

### Phase 1B: Async Compression (Python Service)

**Trigger:** Go service enqueues RabbitMQ job `obs-xyz789` (async, non-blocking)

```
RabbitMQ Queue:
  message = {observationId: "obs-xyz789", sessionId: "sess-abc123"}
         ↓
Python worker consumes:
  POST http://python:5000/compress
    {observationId, sessionId, raw}
         ↓
Python handler:
  1. Extract fields from raw:
     - toolName, toolInput, toolOutput, userPrompt, etc.
  2. If modality = "image":
     - Call LLM vision: describe_image(imageData)
     - imageDescription = "A screenshot of..."
  3. Prepare compression prompt:
     SYSTEM: "Extract facts, concepts, narrative from this observation..."
     USER: f"Tool: {toolName}\nInput: {toolInput}\nOutput: {toolOutput}"
  4. Call LLM (Claude Opus 4):
     response = llm.compress(system, user)  # Timeout 60s
     → Falls back to synthetic if timeout
  5. Parse XML response:
     <type>file_read</type>
     <title>Read auth.ts</title>
     <subtitle>src/middleware/auth.ts</subtitle>
     <facts>
       <fact>Validates JWT tokens</fact>
       <fact>Uses jose library</fact>
     </facts>
     <narrative>Examined authentication middleware...</narrative>
     <concepts>
       <concept>authentication</concept>
       <concept>jwt</concept>
     </concepts>
     <files>
       <file>src/middleware/auth.ts</file>
     </files>
     <importance>7</importance>
  6. Build CompressedObservation:
     {
       id: "obs-xyz789",  // Same as raw
       sessionId: "sess-abc123",
       type: "file_read",
       title: "Read auth.ts",
       subtitle: "src/middleware/auth.ts",
       facts: ["Validates JWT tokens", "Uses jose library"],
       narrative: "Examined authentication middleware...",
       concepts: ["authentication", "jwt"],
       files: ["src/middleware/auth.ts"],
       importance: 7,
       confidence: 0.85,  // LLM quality score
       imageDescription: (optional)
     }
  7. Return to Go:
     POST http://go:3111/internal/observation/{obsId}/compressed
       {compressed: CompressedObservation}
         ↓
Go handler:
  1. SQLite UPDATE: observations[obsId] = compressed (replace raw)
  2. Add to BM25 index: tokenize(title + narrative + concepts)
  3. Embed narrative:
     POST http://embedding-provider/embed
       text = "Read auth.ts examined authentication middleware..."
       → embedding = float32[384] (Xenova) or float32[1536] (OpenAI)
  4. Add to vector index: obsId → embedding
  5. Store in graph: edges for POLE+O types + relationships
  6. Broadcast to viewers
  7. Return 200 OK

Status: CompressedObservation indexed, searchable
```

**Fallback (No LLM):** If `AGENTMEMORY_AUTO_COMPRESS=false`, compress synchronously in Python:

```python
def synthetic_compress(raw):
    # BM25 tokenization
    narrative = f"{toolName} | {stringify(toolInput)} | {truncate(toolOutput, 400)}"
    concepts = []  # Empty (no LLM extraction)
    files = extract_files_regex(raw)  # Regex: /[a-zA-Z0-9._/-]+\.ts/
    facts = []
    
    return CompressedObservation {
        type: infer_type(toolName),
        title: toolName,
        subtitle: files[0] if files else "",
        narrative: narrative,
        concepts: concepts,
        files: files,
        importance: 5,
        confidence: 0.3  # Low confidence (no LLM)
    }
```

---

### Phase 2: Search (Go Service)

**Endpoint:** `POST /agentmemory/smart-search`

```
Client query:
  {
    project: "myproject",
    query: "JWT authentication implementation",
    limit: 10,
    mode: "hybrid"  // or "bm25", "vector", "graph"
  }
         ↓
Go handler:
  
  Stage 1: BM25 Keyword Search
    tokenize("JWT authentication implementation")
    → ["jwt", "authentication", "implementation"]
    → BM25 index lookup: term → [docIds]
    → Score by TF-IDF
    → Return top 30: [(obsId1, score=0.95), (obsId2, score=0.87), ...]
  
  Stage 2: Vector Similarity
    embed("JWT authentication implementation") → float32[384]
    → Cosine similarity against all stored embeddings
    → Return top 30: [(obsId3, sim=0.91), (obsId4, sim=0.84), ...]
  
  Stage 3: Knowledge Graph BFS
    Extract entities from query: ["JWT", "authentication"]
    → Graph lookup: find entities matching (fuzzy)
    → BFS traversal (depth=2): JWT → depends_on → token_validation → ...
    → Return top 20 observations mentioning related entities
  
  Stage 4: Reciprocal Rank Fusion (k=60)
    Combine rankings:
      - BM25: [obsId1 (rank 1), obsId2 (rank 2), ...]
      - Vector: [obsId3 (rank 1), obsId4 (rank 2), ...]
      - Graph: [obsId5 (rank 1), obsId6 (rank 2), ...]
    RRF score = 1/(k + rank_bm25) + 1/(k + rank_vector) + 1/(k + rank_graph)
    → Sort by RRF score
  
  Stage 5: Diversity Boost
    Limit to max 3 results per sessionId (avoid redundancy)
    → Return top 10 combined
  
  Stage 6: Fetch Full Objects
    SQLite SELECT: observations.id IN (top 10 obsIds)
    → Include CompressedObservation metadata
    → Batch Neo4j lookup (optional): enrichment data
  
  Return 200 OK:
    [
      {
        observationId: "obs-xyz789",
        type: "file_read",
        title: "Read auth.ts",
        relevance: 0.92,  // RRF score (normalized)
        snippet: "Examined authentication middleware implementing JWT validation...",
        sessionId: "sess-abc123",
        timestamp: "2026-05-26T12:34:56Z",
        concepts: ["authentication", "jwt"]
      },
      ...
    ]

Status: Results ranked, ready for context injection or display
```

---

### Phase 3: Consolidation (SessionEnd Hook)

**Trigger:** Agent fires `SessionEnd` hook → Go enqueues consolidation job

```
SessionEnd hook:
  POST /agentmemory/observe
    {
      sessionId: "sess-abc123",
      hookType: "session_end",
      data: {summary_hint: "Implemented JWT auth"}
    }
         ↓
Go handler:
  1. RabbitMQ enqueue: {sessionId, force: false}
  2. Return 202 Accepted immediately
  3. Async worker picks up job
         ↓
Consolidation Pipeline (async worker in Go or Python):

  Step 1: Fetch observations
    SQLite SELECT observations WHERE sessionId='sess-abc123'
    → 50+ CompressedObservations
  
  Step 2: Summarize session (Python)
    POST http://python:5000/summarize
      observations: [{title, narrative, concepts}, ...]
    → LLM response:
      SessionSummary {
        title: "Implement JWT authentication to API",
        narrative: "Added middleware for token validation using jose library...",
        keyDecisions: ["Use jose for Edge compat", "1-hour expiry"],
        filesModified: ["src/middleware/auth.ts", ...],
        concepts: ["authentication", "jwt", "security"]
      }
  
  Step 3: Extract entities (Python)
    POST http://python:5000/extract
      observations: [{narrative, title, files}, ...]
    → spaCy NER + GLiNER + LLM fallback
    → Entities with POLE+O types:
      [
        {name: "Jose", type: "OBJECT", subtype: "LIBRARY", confidence: 0.95},
        {name: "auth.ts", type: "OBJECT", subtype: "FILE", confidence: 0.98},
        {name: "validateToken", type: "OBJECT", subtype: "FUNCTION", confidence: 0.92}
      ]
  
  Step 4: Build knowledge graph
    Extract relationships:
      - validateToken DEPENDS_ON jose (0.9)
      - auth.ts IMPLEMENTS middleware (0.95)
      - validateToken EXPORTED_FROM auth.ts (0.98)
    → SQLite INSERT: graph_entities, graph_edges
  
  Step 5: Semantic clustering (Python)
    POST http://python:5000/cluster
      observations: [{facts, concepts, narrative}, ...]
    → LLM groups observations:
      Group 1: "Authentication implementation" (5 obs)
      Group 2: "Testing & validation" (3 obs)
      Group 3: "Deployment" (2 obs)
  
  Step 6: Extract reusable facts (Python)
    For each cluster:
      POST http://python:5000/extract-facts
        observations: [cluster observations]
      → Memory objects:
        Memory {
          type: "architecture",
          title: "Use jose middleware for JWT validation",
          content: "We use jose instead of jsonwebtoken for Edge compatibility. 1-hour expiry.",
          concepts: ["authentication", "jwt", "edge"],
          files: ["src/middleware/auth.ts"],
          sessionIds: ["sess-abc123"],
          strength: 1.0,
          sourceObservationIds: ["obs-xyz789", "obs-xyz790"]
        }
  
  Step 7: Store consolidated data
    SQLite INSERT:
      - sessions_summary[sessionId] = SessionSummary
      - memories[memoryId] = Memory (1..N)
      - graph_entities, graph_edges updated
  
  Step 8: Add to search indexes
    BM25 index: index memories like observations
    Vector index: embed(memory.title + " " + memory.content)
  
  Step 9: Update session
    SQLite UPDATE sessions[sessionId]:
      status = "completed",
      endedAt = now(),
      summary = SessionSummary.narrative

Status: Consolidated, searchable, ready for decay
```

---

### Phase 4: Decay & Auto-Forget (Nightly, Go + Python)

**Trigger:** Cron job (nightly at 2 AM local time)

```
Cron: mem::auto-forget {project: "myproject"}
         ↓
Go scheduler:
  For each Memory:
    daysOld = (now - createdAt) / 86400
    newStrength = strength * (0.95 ^ daysOld)
    
    If newStrength < 0.1:
      SQLite DELETE memories[memoryId]
      → Remove from BM25 + vector indexes
    Else:
      SQLite UPDATE memories[memoryId].strength = newStrength
  
  For each RawObservation (24h TTL):
    If now - createdAt > 24h:
      SQLite DELETE observations[obsId]
  
  For each CompressedObservation (7d TTL):
    If now - createdAt > 7d:
      Neo4j: Move to :ArchivedObservation label
      SQLite: Keep for archive queries
  
  Return {evicted: N, archived: M}

Status: Old memories faded, storage reclaimed
```

---

## API Contracts

### Go ↔ Python (HTTP REST)

**Python Endpoints (listening on `:5000`):**

```
POST /extract
  Input:  {raw: HookPayload.data}
  Output: {entities: [{name, type, subtype, confidence}], relationships: [...]}
  Timeout: 30s
  Fallback: Return empty if timeout

POST /compress
  Input:  {raw, imageData?}
  Output: CompressedObservation {type, title, facts, narrative, concepts, importance, confidence}
  Timeout: 60s
  Fallback: Synthetic compression if LLM timeout

POST /summarize
  Input:  {observations: [{title, narrative, concepts}]}
  Output: SessionSummary {title, narrative, keyDecisions, filesModified, concepts}
  Timeout: 60s

POST /enrich
  Input:  {entity: {name, type}}
  Output: {enriched_description, wikipedia_url, wikidata_id, image_url}
  Timeout: 30s (rate-limited to Wikipedia/Diffbot API limits)

POST /embed
  Input:  {texts: [string]}
  Output: {embeddings: [[float32]]}
  Timeout: 30s

GET /health
  Output: {status: "ok"|"degraded"|"error", up_to_date: bool, model_version: string}

GET /metrics
  Output: Prometheus-formatted metrics
```

**Go Error Handling:**

```python
# Python service returns error
{
  "error": "extraction_timeout",
  "message": "spaCy NER took >30s",
  "fallback": true
}

# Go handler
→ Log warning
→ Use fallback (synthetic compression, empty entities, etc.)
→ Return 202 Accepted (partial/degraded)
```

---

### Neo4j Integration (Bolt Driver)

**Operations:**

```
1. Entity creation:
   CREATE (e:Entity:Person {
     id: uuid,
     name: "Alice",
     type: "PERSON",
     subtype: "INDIVIDUAL",
     embedding: [float32...],
     confidence: 0.95,
     created_at: datetime,
     enriched_description: "...",
     wikipedia_url: "...",
     wikidata_id: "..."
   })

2. Relationship creation:
   MATCH (a:Entity {id: "entity-1"}), (b:Entity {id: "entity-2"})
   CREATE (a)-[:WORKS_AT {confidence: 0.9, source: "obs-xyz789"}]->(b)

3. Search traversal:
   MATCH (e:Entity)-[:DEPENDS_ON*1..2]->(neighbor)
   WHERE e.name =~ "authentication"
   RETURN neighbor, shortestPath((e)-[*]-(neighbor))

4. Enrichment lookup:
   MATCH (e:Entity {name: "Jose"})
   RETURN e.enriched_description, e.wikipedia_url, e.image_url
```

---

## Implementation Phases

### Phase 0: Scaffolding (Week 1)

- [ ] Create Go project skeleton (chi router, SQLite schema)
- [ ] Create Python project skeleton (FastAPI, spaCy + GLiNER models)
- [ ] Docker Compose: Go + Python + Neo4j + RabbitMQ + SQLite
- [ ] GitHub repo with CI/CD (GitHub Actions)
- [ ] Design doc reviews

**Deliverable:** Both services start, basic health checks pass

---

### Phase 1: Core Observation Pipeline (Weeks 2–3)

**Go:**
- [ ] REST API: POST /observe endpoint
- [ ] Validation: required fields, hookType enum
- [ ] Deduplication: 5-min SHA-256 rolling window
- [ ] Privacy filter: regex patterns + <private> tag removal
- [ ] SQLite schema: sessions, observations, metadata
- [ ] Error handling + logging

**Python:**
- [ ] FastAPI setup + /health endpoint
- [ ] Synthetic compression (fallback)
- [ ] Prometheus metrics exporter

**Integration:**
- [ ] Test end-to-end: hook → RawObservation stored

**Deliverable:** Agents can send observations, stored in SQLite

---

### Phase 2: Compression & Search (Weeks 4–6)

**Python:**
- [ ] spaCy + GLiNER model loading
- [ ] LLM extraction pipeline (Claude Opus 4 fallback)
- [ ] LLM compression (with timeout + fallback)
- [ ] XML response parsing
- [ ] Quality scoring

**Go:**
- [ ] RabbitMQ queue setup (async compression)
- [ ] BM25 index (bleve library)
- [ ] Vector index (float32 arrays, cosine similarity)
- [ ] Knowledge graph (entity + edge storage)
- [ ] REST API: POST /smart-search (BM25 + vector + graph RRF)

**Integration:**
- [ ] Test: RawObservation → compression → indexing → search

**Deliverable:** Search works, hybrid ranking functional

---

### Phase 3: Consolidation & Decay (Weeks 7–9)

**Python:**
- [ ] Conversation summarization (LLM)
- [ ] Entity extraction (spaCy → GLiNER → LLM)
- [ ] Semantic clustering (LLM grouping)
- [ ] Fact extraction (LLM)

**Go:**
- [ ] SessionEnd consolidation job
- [ ] 4-tier memory model (Working/Episodic/Semantic/Procedural)
- [ ] Ebbinghaus decay algorithm
- [ ] Consolidation DAG orchestrator
- [ ] Memory strength tracking + eviction

**Neo4j:**
- [ ] Entity CRUD operations (POLE+O types)
- [ ] Relationship extraction + storage
- [ ] Graph traversal queries

**Integration:**
- [ ] Test: SessionEnd → consolidation → Memory created → indexed → decayed

**Deliverable:** Long-term memory working, decay functional

---

### Phase 4: REST API & MCP Server (Weeks 10–12)

**Go:**
- [ ] Complete REST API (124 routes)
  - [ ] Session management (/session/start, /session/end, /sessions)
  - [ ] Observation management (/observations, /observation/:id)
  - [ ] Memory operations (/remember, /forget, /memories)
  - [ ] Team features (/team/share, /team/feed, /audit)
  - [ ] Orchestration (/actions, /frontier, /next, /leases, /routines, /signals)
  - [ ] Consolidation (/consolidate, /timeline, /lessons)
- [ ] MCP server (stdio transport)
  - [ ] 53 tools definitions
  - [ ] 6 resources
  - [ ] 3 prompts
  - [ ] 4 skills (/recall, /remember, /session-history, /forget)
- [ ] HTTP proxy for MCP (fallback if no stdio)

**Deliverable:** All APIs functional, agents can use all tools

---

### Phase 5: Real-Time UI & Observability (Weeks 13–15)

**Go:**
- [ ] WebSocket viewer server (:3113)
  - [ ] Live observation stream
  - [ ] Memory browser
  - [ ] Search results explorer
  - [ ] Session timeline
  - [ ] Knowledge graph visualization
- [ ] OTEL integration (Jaeger/Datadog)
  - [ ] Trace instrumentation (API, search, consolidation)
  - [ ] Metrics (counters, histograms, gauges)
  - [ ] Structured logging (JSON)
- [ ] Health dashboard
  - [ ] Component status
  - [ ] Error rates
  - [ ] Latency percentiles (p50, p95, p99)

**Deliverable:** Production observability complete, UI functional

---

### Phase 6: Production Hardening (Weeks 16–20)

**Go:**
- [ ] Graceful shutdown (signal handling)
- [ ] Connection pooling (SQLite, Neo4j, RabbitMQ)
- [ ] Rate limiting + auth (Bearer token)
- [ ] Input validation + sanitization
- [ ] Comprehensive error codes + messages
- [ ] Backup/restore functionality
- [ ] Migration tooling (schema updates)

**Python:**
- [ ] Circuit breaker (LLM provider failures)
- [ ] Retry logic (exponential backoff)
- [ ] Request timeout enforcement
- [ ] Model loading optimization
- [ ] Batch processing (reduce API calls)

**Testing:**
- [ ] Unit tests (70%+ coverage)
- [ ] Integration tests (Go ↔ Python)
- [ ] End-to-end tests (hook → search → decay)
- [ ] Load tests (1K observations/session, 100 concurrent agents)
- [ ] Chaos tests (service failures, network latency)

**Documentation:**
- [ ] API reference (OpenAPI/Swagger)
- [ ] Deployment guide (Docker, K8s, single-machine)
- [ ] Configuration reference
- [ ] Architecture ADRs (Architecture Decision Records)

**Deliverable:** Production-ready, tested, documented

---

## Directory Structure

```
agent-mem/
├── go/                                 # Go service
│   ├── cmd/
│   │   ├── api/
│   │   │   ├── main.go
│   │   │   └── ...
│   │   └── worker/
│   │       ├── main.go
│   │       └── ...
│   ├── internal/
│   │   ├── api/
│   │   ├── state/
│   │   ├── search/
│   │   ├── privacy/
│   │   ├── consolidation/
│   │   ├── graph/
│   │   ├── mcp/
│   │   ├── models/
│   │   ├── python/
│   │   ├── viewer/
│   │   ├── telemetry/
│   │   └── config/
│   ├── go.mod
│   ├── go.sum
│   ├── Dockerfile
│   ├── docker-compose.yml             # Local dev
│   └── README.md
│
├── py/                                 # Python service
│   ├── agent_mem/
│   │   ├── __init__.py
│   │   ├── api.py
│   │   ├── extraction/
│   │   ├── dedup/
│   │   ├── enrichment/
│   │   ├── compression/
│   │   ├── summarization/
│   │   ├── embedding/
│   │   ├── preferences/
│   │   ├── models/
│   │   ├── providers/
│   │   ├── utils/
│   │   └── config.py
│   ├── tests/
│   │   ├── test_extraction.py
│   │   ├── test_dedup.py
│   │   ├── test_enrichment.py
│   │   └── conftest.py
│   ├── requirements.txt
│   ├── Dockerfile
│   └── README.md
│
├── docs/
│   ├── ARCHITECTURE.md
│   ├── API.md                         # OpenAPI reference
│   ├── DEPLOYMENT.md                  # Docker, K8s, etc.
│   ├── DEVELOPMENT.md                 # Local setup
│   └── ADRs/                          # Architecture Decision Records
│
├── deploy/
│   ├── docker-compose.yml             # Full stack (local)
│   ├── docker-compose.prod.yml        # Production
│   ├── k8s/
│   │   ├── deployment-go.yaml
│   │   ├── deployment-py.yaml
│   │   ├── neo4j-statefulset.yaml
│   │   ├── rabbitmq-statefulset.yaml
│   │   ├── services.yaml
│   │   └── ingress.yaml
│   └── fly.io/                        # Fly.io templates
│
├── .github/
│   └── workflows/
│       ├── go-test.yml
│       ├── py-test.yml
│       ├── docker-build.yml
│       └── integration-test.yml
│
├── FEATURE_ANALYSIS.md                # From earlier work
├── agent_mem.md                       # This file
├── README.md                          # Project overview
└── docker-compose.yml                 # Quick start
```

---

## Key Design Decisions

### 1. Why RabbitMQ for Async Jobs?

**Alternatives:** Redis (simpler), Nats (lighter), SQS (cloud)

**Choice:** RabbitMQ
- **Pro:** Dead-letter queues (failed compressions), priority queues, durable (persistence)
- **Con:** Extra dependency, more ops overhead
- **Mitigation:** Use `--rm` in Docker Compose for local dev; managed RabbitMQ in prod

---

### 2. Why SQLite + Neo4j (Not just Neo4j)?

**Alternatives:** Neo4j only, PostgreSQL + Neo4j, ClickHouse

**Choice:** SQLite (observations) + Neo4j (graph)
- **Pro:** SQLite: lightweight, no server, perfect for observations + KV state; Neo4j: graph queries, enrichment
- **Con:** Dual database complexity, eventual consistency (updates lag)
- **Mitigation:** SQLite for hot path (search), Neo4j for enrichment (async)

---

### 3. Why RRF Fusion (Not ML Reranker)?

**Alternatives:** Cross-encoder reranker (LLM), learned ranking (ML)

**Choice:** RRF (Reciprocal Rank Fusion)
- **Pro:** Deterministic, no training, combines all three signals
- **Con:** Heuristic (not learned), may miss edge cases
- **Mitigation:** Monitor feedback, tune k=60 parameter, add ML reranker in future

---

### 4. Why 4-Tier Memory (Not 3)?

**Alternatives:** 3-tier (Short/Long/Reasoning), 5-tier

**Choice:** 4-tier (Working/Episodic/Semantic/Procedural)
- **Pro:** Mirrors agentmemory design (proven), separates procedures from facts
- **Con:** More complexity
- **Mitigation:** Clear lifetime rules per tier

---

### 5. Why Ebbinghaus Decay?

**Alternatives:** Linear decay, step-wise TTL, manual review

**Choice:** Ebbinghaus curve (strength *= 0.95^daysOld)
- **Pro:** Human-like forgetting, memories reinforced on retrieval
- **Con:** Soft eviction (not guaranteed deletion)
- **Mitigation:** Threshold check (strength < 0.1) for hard eviction

---

### 6. Why Python for Extraction (Not Go)?

**Alternatives:** Go + cgo + Python wrapper, pure Go NLP

**Choice:** Python service (fast loop, simple HTTP bridge)
- **Pro:** spaCy/GLiNER mature, easy to add models, LLM SDKs polished
- **Con:** IPC latency (~50–100ms per call)
- **Mitigation:** Batch operations, cache embeddings, optimize HTTP

---

## Configuration

### Go Service (`.env`)

```bash
# Server
PORT=3111
VIEWER_PORT=3113

# Neo4j
NEO4J_URI=bolt://localhost:7687
NEO4J_USERNAME=neo4j
NEO4J_PASSWORD=password

# SQLite
SQLITE_PATH=~/.agentmemory/data.db

# RabbitMQ
RABBITMQ_URL=amqp://guest:guest@localhost:5672/

# Python Service
PYTHON_SERVICE_URL=http://localhost:5000

# Feature Flags
AGENTMEMORY_AUTO_COMPRESS=false  # Use synthetic by default
AGENTMEMORY_SLOTS=false
AGENTMEMORY_REFLECT=false
CONSOLIDATION_ENABLED=true
LESSON_DECAY_ENABLED=true

# Security
AGENTMEMORY_SECRET=<random-bearer-token>

# Observability
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
LOG_LEVEL=info
```

### Python Service (`.env`)

```bash
# Server
PORT=5000

# LLM Provider (auto-detection chain)
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GEMINI_API_KEY=...

# Embedding Provider
EMBEDDING_PROVIDER=local  # or openai, gemini, voyage
OPENAI_API_KEY=...

# Feature Flags
AGENTMEMORY_AUTO_COMPRESS=false

# Enrichment
WIKIPEDIA_RATE_LIMIT=0.5  # 0.5 req/sec
DIFFBOT_API_KEY=...

# Observability
LOG_LEVEL=info
```

---

## Development Setup

### Quick Start (Docker Compose)

```bash
# Clone repo
git clone https://github.com/yourorg/agent-mem.git
cd agent-mem

# Start all services
docker-compose up

# Verify
curl http://localhost:3111/agentmemory/health
curl http://localhost:5000/health

# Open viewer
open http://localhost:3113
```

### Local Development (Without Docker)

**Prerequisites:**
- Go 1.21+
- Python 3.11+ (with venv)
- Neo4j 5.x (or managed instance)
- RabbitMQ (or use in-memory Redis)

**Go:**
```bash
cd go/
go mod download
go run ./cmd/api/main.go
```

**Python:**
```bash
cd py/
python -m venv venv
source venv/bin/activate
pip install -r requirements.txt
python -m agent_mem.api
```

---

## Testing Strategy

### Unit Tests

**Go:** `go test ./...`
```go
TestObservationValidation
TestDeduplicationWindow
TestPrivacyFilter
TestBM25Indexing
TestVectorSimilarity
TestRRFFusion
```

**Python:** `pytest tests/`
```python
test_spacy_extraction
test_gliner_extraction
test_llm_compression
test_entity_deduplication
test_embedding_providers
```

### Integration Tests

```bash
# Hook → Storage → Compression → Search
test_observation_pipeline

# SessionEnd → Consolidation → Memory
test_consolidation_pipeline

# API → Neo4j → Cypher
test_graph_queries
```

### Load Tests

```bash
# 1K observations/session
# 100 concurrent agents
# Measure: latency (p50, p95, p99), throughput, memory
k6 run tests/load/agent_sim.js
```

---

## Deployment

### Docker Compose (Production Template)

```yaml
version: '3.9'
services:
  go:
    image: agent-mem-go:latest
    ports: ["3111:3111", "3113:3113"]
    environment:
      PYTHON_SERVICE_URL: http://py:5000
      NEO4J_URI: bolt://neo4j:7687
      RABBITMQ_URL: amqp://rabbitmq:5672/
    depends_on: [py, neo4j, rabbitmq]

  py:
    image: agent-mem-py:latest
    ports: ["5000:5000"]
    environment:
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY}
    restart: on-failure

  neo4j:
    image: neo4j:5.x
    ports: ["7687:7687"]
    environment:
      NEO4J_AUTH: neo4j/password
    volumes:
      - neo4j_data:/data

  rabbitmq:
    image: rabbitmq:3.12-management
    ports: ["5672:5672", "15672:15672"]

volumes:
  neo4j_data:
```

### Kubernetes (Optional)

See `deploy/k8s/` for full manifests. Key points:
- Go: Deployment (3 replicas) + Service
- Python: Deployment (2 replicas) + Service
- Neo4j: StatefulSet (1 replica, persistent volume)
- RabbitMQ: StatefulSet (3 replicas, clustering)
- Ingress: Route external traffic to Go API

---

## Observability

### OTEL Traces

Example trace (smart-search):
```
Span: mem::smart-search
  Span: bm25-search (50ms)
  Span: vector-search (120ms)
  Span: graph-traversal (30ms)
  Span: rrf-fusion (5ms)
  Span: fetch-objects (100ms)
Total: 305ms
```

### Metrics

```
# Prometheus
agent_mem_observations_total{project="myproject"} 1234
agent_mem_memories_total{project="myproject"} 89
agent_mem_search_latency_ms{method="smart_search", quantile="0.95"} 145
agent_mem_compression_duration_ms{provider="claude", quantile="0.99"} 2500
agent_mem_llm_api_calls_total{provider="anthropic"} 456
agent_mem_llm_cost_usd{provider="anthropic"} 12.34
```

### Logs

```json
{
  "timestamp": "2026-05-26T14:35:00Z",
  "level": "info",
  "component": "compression",
  "message": "compression completed",
  "observation_id": "obs-xyz789",
  "session_id": "sess-abc123",
  "duration_ms": 450,
  "provider": "claude",
  "confidence": 0.85
}
```

---

## Success Metrics

### Performance
- **Search latency:** p50 < 50ms, p95 < 150ms, p99 < 500ms (for 10K observations)
- **Compression:** avg 2–3s per observation (LLM), 100ms (synthetic)
- **Consolidation:** 5–30s per session (depends on observation count)
- **Memory decay:** nightly job completes in < 1min (100K memories)

### Reliability
- **Uptime:** 99.5% (excluding planned maintenance)
- **Error rate:** < 0.1% (non-transient)
- **Deduplication accuracy:** 99%+ (no missed duplicates)
- **Search recall:** 95%+ at R@10 (on LongMemEval-S)

### Cost (Per Agent Session)
- **LLM tokens:** ~500 tokens/session (compression + summarization + extraction)
- **Embedding API:** ~$0.001 per observation (text-embedding-3-small, if used)
- **Storage:** ~2KB per compressed observation, 5KB per memory
- **Compute:** Go < 100MB RSS, Python < 500MB RSS

---

## Future Extensions

1. **Multi-Agent Coordination:**
   - Extend Signals for task delegation
   - Implement Leases for exclusive action ownership
   - Add Routines for multi-agent workflows

2. **Advanced Consolidation:**
   - Contradiction detection (facts A and B cannot both be true)
   - Supersession tracking (newer facts override older)
   - Cross-session learning (transfer facts between projects)

3. **Governance & Compliance:**
   - GDPR right-to-be-forgotten (safe deletion)
   - HIPAA/SOC2 audit trails (immutable logs)
   - Data residency (regional Neo4j)

4. **Advanced Search:**
   - Learning-to-rank (ML reranker)
   - Query expansion (synonym synonyms)
   - Temporal queries (observations in time window)

5. **Visualization:**
   - Knowledge graph explorer (interactive Neo4j visualization)
   - Timeline playback (replay agent sessions)
   - Comparison (diff two memories)

---

## References

- **reference/DESIGN.md**: Neo4j Agent Memory system (Python)
- **reference/LOW_LEVEL_DESIGN.md**: agentmemory system (Node.js/iii)
- **FEATURE_ANALYSIS.md**: Combined system analysis (this repo)

---

## Next Steps

1. **Review & Validate** this architecture with team
2. **Set up CI/CD** (GitHub Actions for Go + Python)
3. **Create initial scaffold** (Go Chi server, Python FastAPI)
4. **Implement Phase 0** (docker-compose local dev environment)
5. **Begin Phase 1** (observation capture pipeline)

---

**Status**: Architecture & plan complete. Ready for implementation review & Go.
