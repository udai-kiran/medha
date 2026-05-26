# Neo4j Agent Memory - Low Level Design Document

**Version**: 0.4.0  
**Date**: 2026-05-25  
**Authors**: Neo4j Labs

## Table of Contents

1. [Executive Summary](#executive-summary)
2. [Architecture Overview](#architecture-overview)
3. [Core Components](#core-components)
4. [Memory Types and APIs](#memory-types-and-apis)
5. [MCP Tool Interface](#mcp-tool-interface)
6. [Data Model and Neo4j Schema](#data-model-and-neo4j-schema)
7. [Integration Points](#integration-points)
8. [Data Flow](#data-flow)
9. [Backend Support](#backend-support)

---

## Executive Summary

Neo4j Agent Memory is a comprehensive memory system for AI agents with three distinct memory tiers:

- **Short-Term Memory**: Conversation history with messages linked sequentially
- **Long-Term Memory**: Entities (POLE+O), preferences, and facts
- **Reasoning Memory**: Reasoning traces, steps, and tool calls

The system provides:
- **18 MCP Tools** (6 core + 12 extended) for LLM integration
- **Dual Backend Support**: Neo4j Bolt (full-featured) and NAMS (hosted service)
- **Extraction & Resolution**: Entity extraction, deduplication, and fuzzy matching
- **Enrichment**: Wikipedia and Diffbot integration with background processing
- **Observability**: OpenTelemetry and Opik tracing support

---

## Architecture Overview

### High-Level Components

```
┌─────────────────────────────────────────────────────────────┐
│                    MemoryClient                             │
│  (Main entry point, connection management, lifecycle)       │
└──────┬────────────────────────────────────────────────────────┘
       │
       ├─────────────────────┬──────────────────┬─────────────────┐
       │                     │                  │                 │
       ▼                     ▼                  ▼                 ▼
┌─────────────────┐  ┌──────────────────┐  ┌──────────────┐  ┌──────────┐
│  ShortTermMem   │  │  LongTermMem     │  │ ReasoningMem │  │  Query   │
│  (Messages,     │  │  (Entities,      │  │  (Traces,    │  │  (Read   │
│   Conversat.)   │  │   Preferences,   │  │   Steps,     │  │   only)  │
│                 │  │   Facts)         │  │   Tools)     │  │          │
└────────┬────────┘  └────────┬─────────┘  └──────┬───────┘  └────┬─────┘
         │                    │                    │              │
         └────────┬───────────┴────────┬───────────┴──────────────┘
                  │                    │
         ┌────────▼──────────┐  ┌──────▼──────────┐
         │   Neo4jClient     │  │  NamsBackend    │
         │   (Bolt driver)   │  │  (HTTP/REST)    │
         └────────┬──────────┘  └──────┬──────────┘
                  │                    │
    ┌─────────────┴────────┬───────────┴────────────┐
    │                      │                        │
    ▼                      ▼                        ▼
┌─────────────┐   ┌──────────────────┐   ┌──────────────┐
│   Neo4j     │   │   Extraction     │   │ Embeddings   │
│  Database   │   │   Pipeline       │   │ (OpenAI,     │
│             │   │                  │   │  Vertex AI,  │
└─────────────┘   │ (spaCy, GLiNER,  │   │  Bedrock)    │
                  │  LLM)            │   └──────────────┘
                  └──────────────────┘
```

### Supported Backends

#### Bolt Backend (Full-Featured)
- Direct Neo4j driver connection
- All three memory types
- Schema management
- Geocoding and enrichment
- Buffered writes and consolidation
- User management (multi-tenant)
- Evaluation harness

#### NAMS Backend (Hosted Service)
- HTTP REST API
- Cloud-managed extraction/embedding/resolution
- Faster setup, server-managed operational overhead
- No client-side configuration for extraction/embedding
- Limited to protocol surface (short/long/reasoning/query)

---

## Core Components

### 1. MemoryClient

**Location**: `src/neo4j_agent_memory/__init__.py`

Main entry point for all memory operations. Manages connection lifecycle, backend dispatch, and component initialization.

#### Key Properties

```python
async with MemoryClient(settings) as client:
    # Connection properties
    client.is_connected: bool
    client.write_errors: list[dict]
    
    # Memory accessors
    client.short_term: ShortTermMemory
    client.long_term: LongTermMemory
    client.reasoning: ReasoningMemory
    client.query: CypherQueryProtocol
    
    # Advanced features (bolt-only)
    client.schema: SchemaManager
    client.graph: Neo4jClient  # Deprecated in v0.6
    client.users: UserMemory
    client.buffered: BufferedWriter
    client.consolidation: ConsolidationMemory
    client.eval: EvalMemory
```

#### Lifecycle Methods

```python
async client.connect()          # Connect to backend
async client.close()            # Graceful shutdown
async client.flush()            # Drain buffered writes (bolt-only)
async client.wait_for_pending() # Alias of flush()
```

#### Context Methods

```python
# Get combined context from all memory types
context = await client.get_context(
    query="restaurant recommendations",
    session_id="user-123",
    include_short_term=True,
    include_long_term=True,
    include_reasoning=True,
    max_items=10
) -> str

# Get memory statistics
stats = await client.get_stats() -> dict
# Returns: {conversations, messages, entities, preferences, facts, traces}

# Export memory graph for visualization
graph = await client.get_graph(
    memory_types=["short_term", "long_term", "reasoning"],
    session_id="user-123",
    since=datetime(...),
    until=datetime(...),
    include_embeddings=False,
    limit=1000
) -> MemoryGraph

# Get location entities with optional conversation filtering
locations = await client.get_locations(
    session_id="user-123",  # Optional: filter to conversation mentions
    has_coordinates=True,
    limit=500
) -> list[dict]
```

### 2. Neo4jClient

**Location**: `src/neo4j_agent_memory/graph/client.py`

Low-level wrapper around the Neo4j Python driver for bolt backend.

#### Key Methods

```python
async client.connect()                              # Establish connection
async client.close()                                # Close connection
client.is_connected: bool                           # Connection status

# Read operations
await client.execute_read(query, params) -> list    # Execute read-only Cypher
await client.vector_search(...)                    # Vector similarity search

# Write operations (transactional)
await client.execute_write(query, params) -> list   # Execute write Cypher
await client.execute_transaction(tx_func)           # Full transaction control
```

#### Properties

```python
client.driver                                        # Raw Neo4j driver
```

### 3. SchemaManager

**Location**: `src/neo4j_agent_memory/graph/schema.py`

Manages Neo4j database schema: indexes, constraints, node/relationship creation.

#### Key Methods

```python
# Schema setup
await manager.setup_all()                           # Create all indexes/constraints
await manager.setup_indexes()
await manager.setup_constraints()

# Existing graph adoption (v0.2+)
await manager.adopt_existing_graph(
    label_to_type={...},
    name_property_per_label={...}
)

# Validation
await manager.validate_vector_index_dimensions(
    expected_dimensions: int
)

# Schema operations
schemas = await manager.list_schemas()              # List stored schemas
schema = await manager.load_schema(name: str)       # Load by name
await manager.save_schema(config, created_by: str)  # Store in Neo4j
```

---

## Memory Types and APIs

### Short-Term Memory

**Location**: `src/neo4j_agent_memory/memory/short_term.py`

Stores conversation history with temporal relationships and entity mentions.

#### Core Operations

```python
# Add individual message
message = await client.short_term.add_message(
    session_id: str,
    role: MessageRole,          # "user", "assistant", "system"
    content: str,
    metadata: dict | None = None,
    extract_entities: bool = True,
    extract_relations: bool = True,
    user_identifier: str | None = None  # multi-tenant
) -> Message

# Add multiple messages in batch
messages = await client.short_term.add_messages_batch(
    session_id: str,
    messages: list[dict],       # {role, content, metadata?}
    extract_entities: bool = True,
    extract_relations: bool = True,
    user_identifier: str | None = None
) -> list[Message]

# Retrieve conversation
conversation = await client.short_term.get_conversation(
    session_id: str,
    user_identifier: str | None = None
) -> Conversation                # Contains: id, session_id, messages[]

# Search messages by content/embedding
results = await client.short_term.search_messages(
    query: str,
    session_id: str | None = None,
    limit: int = 10,
    threshold: float = 0.7,
    user_identifier: str | None = None
) -> list[Message]

# Get formatted context for LLM
context = await client.short_term.get_context(
    query: str,
    session_id: str | None = None,
    max_messages: int = 5,
    user_identifier: str | None = None
) -> str

# Session management
sessions = await client.short_term.list_sessions(
    limit: int = 100,
    user_identifier: str | None = None
) -> list[SessionInfo]           # id, session_id, message_count, created_at, updated_at, preview

await client.short_term.clear_session(session_id: str) -> None

# Message housekeeping
await client.short_term.delete_message(message_id: str) -> None

await client.short_term.migrate_message_links() -> dict
# Returns: {conversation_id: num_linked, ...}

# Summarization (if LLM provider configured)
summary = await client.short_term.get_conversation_summary(
    session_id: str,
    max_messages: int | None = None
) -> ConversationSummary        # text, key_entities[], generated_at
```

#### Message Models

```python
@dataclass
class Message:
    id: str
    session_id: str
    role: MessageRole              # ENUM: user, assistant, system
    content: str
    metadata: dict[str, Any] | None
    embedding: list[float] | None
    created_at: datetime
    entities: list[Entity] = []    # MENTIONS relationships

@dataclass
class Conversation:
    id: str
    session_id: str
    title: str | None
    created_at: datetime
    updated_at: datetime
    messages: list[Message]
```

### Long-Term Memory

**Location**: `src/neo4j_agent_memory/memory/long_term.py`

Stores entities (POLE+O), preferences, and facts for persistent knowledge.

#### Entity Operations

```python
# Add entity with POLE+O typing
entity, dedup_result = await client.long_term.add_entity(
    name: str,
    entity_type: str,              # PERSON, OBJECT, LOCATION, EVENT, ORGANIZATION
    subtype: str | None = None,    # e.g. VEHICLE, ADDRESS, MEETING, COMPANY
    description: str | None = None,
    metadata: dict | None = None,
    confidence: float = 1.0,
    deduplicate: bool = True,      # Enable deduplication check
    user_identifier: str | None = None
) -> tuple[Entity, DeduplicationResult]

# Search entities by name/embedding
results = await client.long_term.search_entities(
    query: str,
    entity_types: list[str] | None = None,
    limit: int = 10,
    threshold: float = 0.7,
    user_identifier: str | None = None
) -> list[Entity]

# Get entity details with relationships
entity = await client.long_term.get_entity(
    entity_id: str,
    user_identifier: str | None = None
) -> Entity

# Find entities from message
entities = await client.long_term.get_entities_from_message(
    message_id: str
) -> list[tuple[Entity, dict]]     # Entity with provenance info

# Entity deduplication management
await client.long_term.review_duplicate(
    source_id: str,
    target_id: str,
    confirm: bool                   # True to merge, False to reject
) -> None

duplicates = await client.long_term.find_potential_duplicates(
    limit: int = 100
) -> list[tuple[Entity, Entity, float]]  # (source, target, confidence)

cluster = await client.long_term.get_same_as_cluster(
    entity_id: str
) -> list[Entity]                   # All entities in SAME_AS cluster

stats = await client.long_term.get_deduplication_stats() -> dict
# Returns: {total_entities, merged_entities, pending_reviews}

# Entity metadata/enrichment
await client.long_term.add_enrichment(
    entity_id: str,
    enriched_description: str | None = None,
    wikipedia_url: str | None = None,
    wikidata_id: str | None = None,
    image_url: str | None = None
) -> None

# Geocoding for locations
location = await client.long_term.add_entity(
    name="Empire State Building",
    entity_type="LOCATION",
    coordinates=(40.7484, -73.9857),  # (lat, lon) explicit
    geocode=True                       # or auto-geocode if geocoder configured
) -> tuple[Entity, DeduplicationResult]

await client.long_term.geocode_locations(
    skip_existing: bool = True
) -> dict                              # {processed, geocoded, skipped, failed}

coords = await client.long_term.get_location_coordinates(
    entity_id: str
) -> tuple[float, float] | None        # (latitude, longitude)

nearby = await client.long_term.search_locations_near(
    latitude: float,
    longitude: float,
    radius_km: float = 5.0,
    limit: int = 10
) -> list[Entity]

locations = await client.long_term.search_locations_in_bounding_box(
    min_lat: float, min_lon: float,
    max_lat: float, max_lon: float
) -> list[Entity]

# Relationship extraction and creation
await client.long_term.create_relationship(
    source_entity: Entity,
    target_entity: Entity,
    relation_type: str,              # e.g. WORKS_AT, KNOWS, LOCATED_IN
    confidence: float = 1.0
) -> None

# Entity provenance tracking
await client.long_term.register_extractor(
    name: str,
    version: str,
    config: dict | None = None
) -> None

await client.long_term.link_entity_to_message(
    entity: Entity,
    message_id: str,
    confidence: float = 1.0,
    start_pos: int | None = None,
    end_pos: int | None = None,
    context: str | None = None
) -> None

await client.long_term.link_entity_to_extractor(
    entity: Entity,
    extractor_name: str,
    confidence: float = 1.0,
    extraction_time_ms: float | None = None
) -> None

provenance = await client.long_term.get_entity_provenance(
    entity: Entity
) -> dict                              # {sources: [...], extractors: [...]}

extractors = await client.long_term.list_extractors() -> list[dict]
# Returns: [{name, version, entity_count, created_at}, ...]

stats = await client.long_term.get_extraction_stats() -> dict
# Returns: {total_entities, source_messages, ...}

await client.long_term.delete_entity_provenance(
    entity: Entity
) -> int                               # count of deleted provenance links
```

#### Preference Operations

```python
# Add user preference
preference = await client.long_term.add_preference(
    category: str,                 # "food", "music", "travel", etc.
    preference: str,
    confidence: float = 1.0,
    metadata: dict | None = None,
    user_identifier: str | None = None
) -> Preference

# Search preferences
results = await client.long_term.search_preferences(
    query: str,
    category: str | None = None,
    limit: int = 10,
    user_identifier: str | None = None
) -> list[Preference]

# Get all preferences for user
prefs = await client.long_term.get_preferences(
    category: str | None = None,
    user_identifier: str | None = None
) -> list[Preference]

# Delete preference
await client.long_term.delete_preference(preference_id: str) -> None
```

#### Fact Operations

```python
# Add subject-predicate-object fact
fact = await client.long_term.add_fact(
    subject: str,
    predicate: str,               # relation type
    object_value: str,            # object entity/value
    confidence: float = 1.0,
    user_identifier: str | None = None
) -> Fact

# Search facts
results = await client.long_term.search_facts(
    query: str,
    subject: str | None = None,
    predicate: str | None = None,
    limit: int = 10,
    user_identifier: str | None = None
) -> list[Fact]
```

#### Context and Search

```python
# Get formatted context for LLM
context = await client.long_term.get_context(
    query: str,
    max_items: int = 5,
    user_identifier: str | None = None
) -> str

# Full-text search across entities, preferences, facts
results = await client.long_term.search(
    query: str,
    limit: int = 10,
    user_identifier: str | None = None
) -> list[Entity | Preference | Fact]
```

#### Entity Models

```python
@dataclass
class Entity:
    id: str
    name: str
    type: str                      # PERSON, OBJECT, LOCATION, EVENT, ORGANIZATION
    subtype: str | None            # e.g. VEHICLE, ADDRESS, INDIVIDUAL
    full_type: str                 # Computed: "{type}:{subtype}" or just type
    description: str | None
    metadata: dict[str, Any] | None
    embedding: list[float] | None
    confidence: float
    created_at: datetime
    # Enrichment fields (from Wikipedia/Diffbot)
    enriched_description: str | None
    wikipedia_url: str | None
    wikidata_id: str | None
    image_url: str | None
    # Geocoding (for LOCATION type)
    location: Point | None         # Neo4j Point with lat/lon

@dataclass
class Preference:
    id: str
    category: str
    preference: str
    confidence: float
    metadata: dict[str, Any] | None
    created_at: datetime
    updated_at: datetime

@dataclass
class Fact:
    id: str
    subject: str
    predicate: str
    object_value: str
    confidence: float
    created_at: datetime
```

### Reasoning Memory

**Location**: `src/neo4j_agent_memory/memory/reasoning.py`

Stores reasoning traces, steps, and tool invocations for decision audit trails.

#### Trace Operations

```python
# Start a reasoning trace
trace = await client.reasoning.start_trace(
    session_id: str,
    task: str,                     # What are we reasoning about?
    triggered_by_message_id: str | None = None,  # Link to message
    metadata: dict | None = None,
    user_identifier: str | None = None
) -> ReasoningTrace               # id, session_id, task, started_at, steps[]

# Add reasoning step to trace
step = await client.reasoning.record_step(
    trace_id: str,
    thought: str,                  # The reasoning
    action: str | None = None,    # What action was taken
    observation: str | None = None,
    user_identifier: str | None = None
) -> ReasoningStep

# Record tool invocation within a step
await client.reasoning.record_tool_call(
    step_id: str,
    tool_name: str,
    arguments: dict,
    result: Any,
    status: ToolCallStatus = ToolCallStatus.SUCCESS,
    error: str | None = None,
    execution_time_ms: float | None = None,
    message_id: str | None = None,  # Link to triggering message
    touched_entities: list[EntityRef] | None = None,  # Audit: what did this affect?
    user_identifier: str | None = None
) -> ToolCall

# Complete trace with outcome
await client.reasoning.complete_trace(
    trace_id: str,
    outcome: str,                  # Summary of reasoning result
    success: bool = True,
    user_identifier: str | None = None
) -> None

# Link trace to message (post-hoc)
await client.reasoning.link_trace_to_message(
    trace_id: str,
    message_id: str
) -> None

# Retrieve trace
trace = await client.reasoning.get_trace(
    trace_id: str,
    user_identifier: str | None = None
) -> ReasoningTrace             # Full trace with steps and tool calls

# Search traces
traces = await client.reasoning.search_traces(
    query: str,
    session_id: str | None = None,
    limit: int = 10,
    user_identifier: str | None = None
) -> list[ReasoningTrace]

# Get formatted context for LLM (similar past tasks)
context = await client.reasoning.get_context(
    query: str,
    max_traces: int = 3,
    user_identifier: str | None = None
) -> str

# Trace statistics
stats = await client.reasoning.get_stats() -> ToolStats
# Returns: {tool_count, total_calls, success_count, error_count, avg_execution_time_ms}
```

#### Streaming Trace Recorder

For long-running tasks that emit steps in real-time:

```python
async with client.reasoning.streaming_trace_recorder(
    session_id: str,
    task: str
) as recorder:
    step = await recorder.record_step("Analyzing input...", "analyze")
    
    # ... do work ...
    
    await recorder.record_tool_call(
        step.id,
        tool_name="search_api",
        arguments={"query": "..."},
        result=[...],
        status=ToolCallStatus.SUCCESS
    )
    
    # Trace is auto-completed on exit
```

#### Models

```python
@dataclass
class ReasoningTrace:
    id: str
    session_id: str
    task: str
    started_at: datetime
    completed_at: datetime | None
    success: bool
    outcome: str | None
    steps: list[ReasoningStep]
    metadata: dict[str, Any] | None
    task_embedding: list[float] | None

@dataclass
class ReasoningStep:
    id: str
    trace_id: str
    thought: str
    action: str | None
    observation: str | None
    index: int                      # Order in trace
    created_at: datetime
    tool_calls: list[ToolCall]

@dataclass
class ToolCall:
    id: str
    step_id: str
    tool_name: str
    arguments: dict[str, Any]
    result: Any
    status: ToolCallStatus          # ENUM: SUCCESS, ERROR, PARTIAL
    error: str | None
    execution_time_ms: float | None
    created_at: datetime

@dataclass
class EntityRef:
    entity_id: str
    relation: str                   # How the entity was touched
```

---

## MCP Tool Interface

**Location**: `src/neo4j_agent_memory/mcp/_tools.py`

MCP (Model Context Protocol) tools expose memory operations to LLMs. Organized into profiles:

### Core Profile (6 Tools)

Minimal set for essential read/write cycle:

#### 1. `memory_search` *(READ)*
Hybrid vector + graph search across all memory types.

```
memory_search(
    query: str,                    # Search query
    limit: int = 10,               # Results per type
    memory_types: list[str] | None = ["messages", "entities", "preferences"],
    session_id: str | None = None,
    threshold: float = 0.7
) -> str                           # Formatted results
```

#### 2. `memory_get_context` *(READ)*
Assembled context for a session (conversation + entities + preferences).

```
memory_get_context(
    session_id: str,
    include_short_term: bool = True,
    include_long_term: bool = True,
    include_reasoning: bool = False,
    max_items: int = 5
) -> str                           # Formatted context
```

#### 3. `memory_store_message` *(WRITE)*
Store message with auto entity extraction and preference detection.

```
memory_store_message(
    session_id: str,
    role: str,                     # "user", "assistant", "system"
    content: str,
    extract_entities: bool = True,
    extract_relations: bool = True
) -> str                           # Message ID and summary
```

#### 4. `memory_add_entity` *(WRITE)*
Create/update entity with POLE+O typing and resolution.

```
memory_add_entity(
    name: str,
    entity_type: str,              # PERSON, OBJECT, LOCATION, EVENT, ORGANIZATION
    subtype: str | None = None,
    description: str | None = None,
    confidence: float = 1.0
) -> str                           # Entity summary
```

#### 5. `memory_add_preference` *(WRITE)*
Record user preference with category.

```
memory_add_preference(
    category: str,                 # "food", "music", etc.
    preference: str,
    confidence: float = 1.0
) -> str                           # Preference summary
```

#### 6. `memory_add_fact` *(WRITE)*
Store subject-predicate-object triple.

```
memory_add_fact(
    subject: str,
    predicate: str,                # Relation type
    object_value: str,
    confidence: float = 1.0
) -> str                           # Fact summary
```

### Extended Profile (12 Additional Tools)

Full surface including advanced queries and reasoning:

#### 7. `memory_get_conversation` *(READ)*
Full conversation history for a session.

```
memory_get_conversation(
    session_id: str,
    include_entities: bool = True,
    limit: int = 100
) -> str                           # Formatted conversation
```

#### 8. `memory_list_sessions` *(READ)*
List sessions with message counts and previews.

```
memory_list_sessions(
    limit: int = 50,
    offset: int = 0
) -> str                           # Session list
```

#### 9. `memory_get_entity` *(READ)*
Entity details with graph relationship traversal.

```
memory_get_entity(
    entity_id: str,
    include_relationships: bool = True,
    relationship_depth: int = 1    # How far to traverse
) -> str                           # Entity details
```

#### 10. `memory_export_graph` *(READ)*
Subgraph export as JSON for visualization.

```
memory_export_graph(
    memory_types: list[str] = ["short_term", "long_term"],
    session_id: str | None = None,
    limit: int = 1000
) -> str                           # JSON graph (nodes, relationships)
```

#### 11. `memory_create_relationship` *(WRITE)*
Typed relationship between entities.

```
memory_create_relationship(
    source_entity_id: str,
    target_entity_id: str,
    relation_type: str,            # WORKS_AT, KNOWS, LOCATED_IN, etc.
    confidence: float = 1.0
) -> str                           # Relationship summary
```

#### 12. `memory_start_trace` *(WRITE)*
Begin reasoning trace recording.

```
memory_start_trace(
    session_id: str,
    task: str,                     # What are we reasoning about?
    metadata: dict | None = None
) -> str                           # Trace ID
```

#### 13. `memory_record_step` *(WRITE)*
Record reasoning step with optional tool call.

```
memory_record_step(
    trace_id: str,
    thought: str,                  # The reasoning
    action: str | None = None,
    observation: str | None = None,
    tool_call: dict | None = None  # {tool_name, arguments, result, status}
) -> str                           # Step ID
```

#### 14. `memory_complete_trace` *(WRITE)*
Complete trace with outcome.

```
memory_complete_trace(
    trace_id: str,
    outcome: str,                  # Summary of result
    success: bool = True
) -> str                           # Trace summary
```

#### 15. `memory_get_observations` *(READ)*
Session observations from observational memory.

```
memory_get_observations(
    session_id: str,
    observation_type: str | None = None  # "reflection", "observation"
) -> str                           # Formatted observations
```

#### 16. `graph_query` *(READ)*
Read-only Cypher queries (with validation).

```
graph_query(
    query: str,                    # Cypher query (read-only enforced)
    parameters: dict | None = None
) -> str                           # Query results as formatted string/JSON
```

### Platinum Profile (4 NAMS-Only Tools)

Available only on NAMS backend:

#### 17. `memory_set_entity_feedback` *(WRITE)*
Rate or correct entity extraction.

```
memory_set_entity_feedback(
    entity_id: str,
    feedback_type: str,            # "correct", "incorrect", "relevant"
    notes: str | None = None
) -> str
```

#### 18. `memory_get_entity_history` *(READ)*
Entity change history and versions.

```
memory_get_entity_history(
    entity_id: str,
    limit: int = 50
) -> str                           # Change log
```

#### 19. `memory_get_entity_provenance` *(READ)*
Where entity came from (NAMS-managed).

```
memory_get_entity_provenance(
    entity_id: str
) -> str                           # Provenance info
```

#### 20. `memory_get_reflections` *(READ)*
High-level reflections on session.

```
memory_get_reflections(
    session_id: str
) -> str                           # Reflection text
```

---

## Data Model and Neo4j Schema

### Node Types

#### Short-Term Memory

```cypher
(:Conversation {
    id: UUID,
    session_id: String,             # User session identifier
    title: String | NULL,
    created_at: DateTime,
    updated_at: DateTime
})

(:Message {
    id: UUID,
    session_id: String,
    role: String,                   # "user", "assistant", "system"
    content: String,
    metadata: String | NULL,        # JSON serialized
    embedding: List[Float],         # Vector index
    created_at: DateTime
})
```

#### Long-Term Memory

```cypher
(:Entity {
    id: UUID,
    name: String,
    type: String,                   # PERSON, OBJECT, LOCATION, EVENT, ORGANIZATION
    subtype: String | NULL,
    description: String | NULL,
    metadata: String | NULL,        # JSON serialized
    embedding: List[Float],         # Vector index
    confidence: Float,
    created_at: DateTime,
    updated_at: DateTime,
    
    # Enrichment fields (from Wikipedia/Diffbot)
    enriched_description: String | NULL,
    wikipedia_url: String | NULL,
    wikidata_id: String | NULL,
    image_url: String | NULL,
    
    # Geocoding (for LOCATION entities)
    location: Point | NULL          # Neo4j Point(latitude, longitude)
})

(:Entity:Person:Individual {...})   # Dynamic labels for type/subtype
(:Entity:Object:Vehicle {...})      # Example: OBJECT:VEHICLE

(:Preference {
    id: UUID,
    category: String,
    preference: String,
    confidence: Float,
    metadata: String | NULL,
    created_at: DateTime,
    updated_at: DateTime
})

(:Fact {
    id: UUID,
    subject: String,
    predicate: String,
    object: String,
    confidence: Float,
    created_at: DateTime
})

(:Extractor {
    id: UUID,
    name: String,                   # e.g., "GLiNEREntityExtractor"
    version: String,
    config: String | NULL,          # JSON serialized
    created_at: DateTime
})
```

#### Reasoning Memory

```cypher
(:ReasoningTrace {
    id: UUID,
    session_id: String,
    task: String,
    started_at: DateTime,
    completed_at: DateTime | NULL,
    success: Boolean,
    outcome: String | NULL,
    task_embedding: List[Float]     # Vector index for search
})

(:ReasoningStep {
    id: UUID,
    trace_id: String,
    thought: String,
    action: String | NULL,
    observation: String | NULL,
    index: Integer,
    created_at: DateTime
})

(:ToolCall {
    id: UUID,
    step_id: String,
    tool_name: String,
    arguments: String,              # JSON serialized
    result: String,                 # JSON serialized
    status: String,                 # SUCCESS, ERROR, PARTIAL
    error: String | NULL,
    execution_time_ms: Float | NULL,
    created_at: DateTime
})

(:Tool {
    name: String,                   # Unique tool identifier
    description: String | NULL
})
```

### Relationship Types

#### Short-Term Relationships

```cypher
(Conversation)-[:FIRST_MESSAGE]->(Message)      # O(1) access to first
(Conversation)-[:HAS_MESSAGE]->(Message)        # Backward compat
(Message)-[:NEXT_MESSAGE]->(Message)            # Sequential chain
(Message)-[:MENTIONS]->(Entity)                 # Entity mentions
```

#### Long-Term Relationships

```cypher
(Entity)-[:RELATED_TO {
    relation_type: String,          # WORKS_AT, KNOWS, LOCATED_IN, etc.
    confidence: Float
}]->(Entity)

(Entity)-[:SAME_AS {
    confidence: Float,
    match_type: String,             # "embedding", "fuzzy", "both"
    status: String,                 # "pending", "confirmed", "rejected"
    created_at: DateTime
}]->(Entity)                        # Deduplication cluster

(Entity)-[:EXTRACTED_FROM {
    confidence: Float,
    start_pos: Integer,
    end_pos: Integer,
    context: String | NULL
}]->(Message)                       # Provenance: where extracted

(Entity)-[:EXTRACTED_BY {
    confidence: Float,
    extraction_time_ms: Float
}]->(Extractor)                     # Which extractor created it
```

#### Cross-Memory Relationships

```cypher
(ReasoningTrace)-[:INITIATED_BY]->(Message)     # What triggered trace
(ReasoningTrace)-[:HAS_STEP]->(ReasoningStep)
(ReasoningStep)-[:USES_TOOL]->(ToolCall)
(ReasoningStep)-[:TOUCHED]->(Entity)            # v0.2: Audit edge
(ToolCall)-[:INSTANCE_OF]->(Tool)
(ToolCall)-[:TRIGGERED_BY]->(Message)           # What triggered call
```

### Indexes

```cypher
CREATE INDEX conversation_session_id ON (:Conversation) (session_id)
CREATE INDEX message_embedding ON (:Message) (embedding) OPTIONS {indexType: 'vector'}
CREATE INDEX entity_name ON (:Entity) (name)
CREATE INDEX entity_embedding ON (:Entity) (embedding) OPTIONS {indexType: 'vector'}
CREATE INDEX entity_type ON (:Entity) (type)
CREATE INDEX entity_location ON (:Entity) (location)
CREATE INDEX preference_category ON (:Preference) (category)
CREATE INDEX reasoning_trace_embedding ON (:ReasoningTrace) (task_embedding) OPTIONS {indexType: 'vector'}
```

---

## Integration Points

### 1. MCP Server Integration

**Location**: `src/neo4j_agent_memory/mcp/server.py`

Exposes memory tools to LLMs via Model Context Protocol.

```python
# Factory for easy creation
server = create_mcp_server(
    settings=MemorySettings(...),
    profile="extended",             # "core" or "extended"
    register_platinum=False,        # NAMS Platinum tools
)

# Or manual instantiation
async with MemoryClient(settings) as client:
    server = Neo4jMemoryMCPServer(client, profile="extended")
    await server.run_async(transport="stdio")
```

### 2. MemoryIntegration Convenience Layer

**Location**: `src/neo4j_agent_memory/integration.py`

High-level wrapper used by MCP server and applications.

```python
async with MemoryIntegration(
    neo4j_uri="bolt://localhost:7687",
    neo4j_password="password",
    session_strategy=SessionStrategy.PER_DAY,  # or PER_CONVERSATION, PERSISTENT
    user_id="alice",
    auto_extract=True,              # Auto-extract entities
    auto_preferences=True,          # Auto-detect preferences
) as memory:
    # Unified high-level API
    await memory.store_message("user", "I love Italian food")
    context = await memory.get_context()
    results = await memory.search("food preferences")
```

### 3. Extraction Pipeline

**Location**: `src/neo4j_agent_memory/extraction/`

Multi-stage entity/relationship extraction:

```python
from neo4j_agent_memory.extraction import (
    ExtractionPipeline,
    GLiNEREntityExtractor,
    SpacyEntityExtractor,
    ExtractorBuilder,
)

# Factory approach
extractor = ExtractorBuilder()
    .with_spacy("en_core_web_sm")
    .with_gliner(schema="poleo", threshold=0.5)
    .with_llm_fallback("gpt-4o-mini")
    .merge_by_confidence()
    .build()

result = await extractor.extract("John works at Acme Corp in NYC")
# Returns: ExtractionResult with entities and relations

# Batch extraction
results = await extractor.extract_batch(
    texts=["text 1", "text 2", ...],
    batch_size=10,
    max_concurrency=5
)

# Streaming for long documents
streamer = StreamingExtractor(extractor, chunk_size=4000)
async for chunk_result in streamer.extract_streaming(long_text):
    print(f"Chunk {chunk_result.chunk.index}: {chunk_result.entity_count} entities")
```

### 4. Entity Resolution

**Location**: `src/neo4j_agent_memory/resolution/`

Deduplication strategies:

```python
from neo4j_agent_memory.resolution import CompositeResolver

resolver = CompositeResolver(
    embedder=embedder,
    exact_threshold=0.99,
    fuzzy_threshold=0.90,
    semantic_threshold=0.85
)

# Used internally when adding entities
entity, dedup_result = await client.long_term.add_entity(...)
if dedup_result.action == "merged":
    print(f"Merged with {dedup_result.matched_entity_name}")
```

### 5. Embedding Providers

**Location**: `src/neo4j_agent_memory/embeddings/`

Support for multiple embedding services:

```python
from neo4j_agent_memory import MemorySettings, EmbeddingProvider

# OpenAI (default)
settings = MemorySettings(
    embedding=EmbeddingConfig(
        provider=EmbeddingProvider.OPENAI,
        model="text-embedding-3-small",
        dimensions=1536
    )
)

# Vertex AI (Google Cloud)
from neo4j_agent_memory.embeddings.vertex_ai import VertexAIEmbedder
embedder = VertexAIEmbedder(
    model="text-embedding-004",
    project_id="my-gcp-project",
    location="us-central1"
)

# Bedrock (AWS)
settings = MemorySettings(
    embedding=EmbeddingConfig(
        provider=EmbeddingProvider.BEDROCK,
        model="cohere.embed-english-v3",
        region="us-east-1"
    )
)
```

### 6. Enrichment Providers

**Location**: `src/neo4j_agent_memory/enrichment/`

Background entity enrichment from Wikipedia/Diffbot:

```python
from neo4j_agent_memory import EnrichmentConfig, EnrichmentProvider

settings = MemorySettings(
    enrichment=EnrichmentConfig(
        enabled=True,
        providers=[EnrichmentProvider.WIKIMEDIA],  # Free
        background_enabled=True,
        entity_types=["PERSON", "ORGANIZATION"],
        min_confidence=0.7
    )
)
# Entities auto-enriched with Wikipedia data in background
```

### 7. Geocoding

**Location**: `src/neo4j_agent_memory/services/geocoder.py`

Location entity geocoding:

```python
from neo4j_agent_memory import GeocodingConfig, GeocodingProvider

settings = MemorySettings(
    geocoding=GeocodingConfig(
        enabled=True,
        provider=GeocodingProvider.NOMINATIM,  # Free
        # provider=GeocodingProvider.GOOGLE,   # Requires API key
        cache_results=True
    )
)

location = await client.long_term.add_entity(
    "Central Park, New York",
    "LOCATION",
    geocode=True  # Auto-geocode if configured
)
# Entity now has location.latitude and location.longitude
```

### 8. LLM Providers

**Location**: `src/neo4j_agent_memory/llm/`

Support for multiple LLMs (for summarization, extraction):

```python
from neo4j_agent_memory import MemorySettings, LLMProvider

settings = MemorySettings(
    llm=LLMProvider.OPENAI,  # For LLM-based extraction, summarization
    # or llm=LLMProvider.ANTHROPIC
)
```

---

## Data Flow

### 1. Message Storage with Entity Extraction

```
User Input Message
        │
        ▼
add_message(session_id, role, content, extract_entities=True)
        │
        ├─► Message node created
        │
        ├─► Extract entities (Extraction Pipeline)
        │   ├─ spaCy NER → GLiNER → LLM fallback
        │   └─ Optional relationship extraction (GLiREL)
        │
        ├─► Resolve extracted entities (CompositeResolver)
        │   ├─ Exact match check
        │   ├─ Fuzzy match check
        │   └─ Semantic match (embeddings)
        │
        ├─► Check for duplicates (DeduplicationConfig)
        │   ├─ Auto-merge if confidence > 0.95
        │   └─ Flag for review if 0.85-0.95
        │
        ├─► Create Entity nodes (if new)
        │   └─ Add POLE+O type labels
        │
        ├─► Link relationships
        │   ├─ Message -[:MENTIONS]-> Entity
        │   └─ Entity -[:RELATED_TO]-> Entity
        │
        ├─► Trigger background enrichment (if enabled)
        │   ├─ Wikipedia lookup
        │   └─ Diffbot knowledge graph
        │
        ├─► Geocode locations (if enabled)
        │
        └─► Generate embedding & index
            └─ Vector search index
```

### 2. Entity Search and Retrieval

```
Search Query
        │
        ▼
search_entities(query, entity_types=["PERSON", "LOCATION"])
        │
        ├─ Vector similarity search (embedding-based)
        │  ├─ Encode query
        │  └─ Find similar entity embeddings
        │
        ├─ Cypher index lookup (exact/fuzzy)
        │
        └─ Merge & deduplicate results
            └─ Return ranked Entity objects with metadata
```

### 3. Reasoning Trace Recording

```
Task Start
        │
        ▼
start_trace(session_id, task)
        │
        ├─ ReasoningTrace node created
        │
        ├─ LOOP for each reasoning step:
        │  │
        │  ├─► record_step(thought, action, observation)
        │  │   └─ ReasoningStep node created
        │  │
        │  └─► record_tool_call(tool_name, arguments, result)
        │      ├─ ToolCall node created
        │      ├─ Link: ReasoningStep -[:USES_TOOL]-> ToolCall
        │      └─ Optional: Link touched entities for audit
        │
        └─ complete_trace(outcome, success)
            └─ ReasoningTrace marked complete
```

### 4. Context Assembly (get_context)

```
get_context(query, session_id)
        │
        ├─► Short-Term: search_messages(query, session_id)
        │   └─ Return recent messages from conversation
        │
        ├─► Long-Term: search_entities(query)
        │   ├─ Find related entities
        │   └─ Retrieve preferences & facts
        │
        ├─► Reasoning: search_traces(query)
        │   └─ Find similar past tasks
        │
        └─ Format & combine results
            └─ Return assembled context string for LLM
```

---

## Backend Support

### Bolt Backend

Full-featured Neo4j driver connection.

**Connection**:
```python
settings = MemorySettings(
    neo4j={"uri": "bolt://localhost:7687", "password": "password"}
)
```

**Supported Features**:
- All three memory types
- Schema management & adoption
- Geocoding & enrichment
- Buffered writes & consolidation
- User management (multi-tenant)
- Evaluation harness
- Direct graph query access

**Implementation**:
- `Neo4jClient`: Low-level bolt driver wrapper
- `ShortTermMemory`, `LongTermMemory`, `ReasoningMemory`: Direct Neo4j implementations
- `SchemaManager`: Index/constraint management

### NAMS Backend

Hosted Neo4j Agent Memory Service (cloud-managed).

**Connection**:
```python
settings = MemorySettings(
    backend="nams",
    nams={"api_key": "nams-api-key", "org_id": "org-id", ...}
)
```

**Supported Features**:
- All three memory types (via HTTP REST API)
- Server-managed extraction/embedding/resolution
- Read-only Cypher queries
- No client-side configuration needed

**Implementation**:
- `NamsBackend`: HTTP REST client
- `NamsShortTermMemory`, `NamsLongTermMemory`, `NamsReasoningMemory`: REST-based implementations
- Protocol-based design allows transparent backend switching

**Unsupported on NAMS**:
- Schema management (server-managed)
- Buffered writes (server commits synchronously)
- User management (per-conversation via userId)
- Consolidation jobs
- Direct graph access (use `client.query.cypher()` instead)

---

## Configuration Management

### MemorySettings (Pydantic)

```python
from neo4j_agent_memory import MemorySettings

settings = MemorySettings(
    # Backend selection
    backend="bolt",              # or "nams"
    
    # Neo4j connection (bolt-only)
    neo4j=Neo4jConfig(
        uri="bolt://localhost:7687",
        username="neo4j",
        password=SecretStr("password"),
        max_connection_pool_size=50,
        connection_timeout_seconds=30
    ),
    
    # NAMS connection (nams-only)
    nams=NamsConfig(
        api_key=SecretStr("..."),
        org_id="org-123",
        validate_on_connect=True
    ),
    
    # Embeddings
    embedding=EmbeddingConfig(
        provider=EmbeddingProvider.OPENAI,
        model="text-embedding-3-small",
        dimensions=1536,
        batch_size=128
    ),
    
    # Extraction
    extraction=ExtractionConfig(
        extractor_type=ExtractorType.PIPELINE,
        enable_spacy=True,
        enable_gliner=True,
        enable_llm_fallback=True,
        gliner_schema="poleo"
    ),
    
    # Entity resolution
    resolution=ResolutionConfig(
        strategy=ResolverStrategy.COMPOSITE,
        exact_threshold=0.99,
        fuzzy_threshold=0.90,
        semantic_threshold=0.85
    ),
    
    # Memory options
    memory=MemoryConfig(
        multi_tenant=False,
        write_mode="sync",           # or "buffered"
        max_pending=1000
    ),
    
    # Search
    search=SearchConfig(
        default_limit=10,
        default_threshold=0.7
    ),
    
    # Geocoding (Location entities)
    geocoding=GeocodingConfig(
        enabled=True,
        provider=GeocodingProvider.NOMINATIM,
        cache_results=True,
        rate_limit_per_second=1
    ),
    
    # Enrichment (Entity enrichment)
    enrichment=EnrichmentConfig(
        enabled=True,
        providers=[EnrichmentProvider.WIKIMEDIA],
        background_enabled=True,
        entity_types=["PERSON", "ORGANIZATION"],
        min_confidence=0.7
    ),
    
    # LLM (for summarization, extraction)
    llm=LLMConfig(
        provider=LLMProvider.OPENAI,
        model="gpt-4o-mini",
        api_key=SecretStr("...")
    )
)
```

---

## Error Handling

Exception hierarchy:

```python
MemoryError (base)
├─ ConnectionError           # Backend connection failed
├─ SchemaError              # Database schema issue
├─ ExtractionError          # Entity extraction failed
├─ ResolutionError          # Entity deduplication failed
├─ EmbeddingError           # Vector embedding failed
├─ ConfigurationError       # Invalid configuration
├─ NotConnectedError        # Client not connected
├─ AuthenticationError      # NAMS auth failed
├─ NotSupportedError        # Feature not available on backend
├─ RateLimitError           # API rate limit hit
├─ TransportError           # NAMS HTTP transport error
└─ ValidationError          # Input validation failed
```

Usage:

```python
from neo4j_agent_memory import MemoryClient, NotConnectedError

try:
    async with MemoryClient(settings) as client:
        entity = await client.long_term.add_entity(...)
except NotConnectedError as e:
    # Handle connection issue
    print(f"Client not connected: {e}")
except ExtractionError as e:
    # Handle extraction failure
    print(f"Extraction failed: {e}")
```

---

## Memory Maintenance and Lifecycle

### Conversation Lifecycle

#### Short-Term Memory Retention

Conversations and messages are retained indefinitely by default. Optional TTL-based archival:

```python
from neo4j_agent_memory.memory.consolidation import ConsolidationMemory

# Dry-run: show what would be archived
stats = await client.consolidation.archive_expired_conversations(
    ttl_days=90,           # Archive conversations older than 90 days
    dry_run=True           # Show what would happen without modifying
) -> dict                  # {archived_count, messages_archived}

# Execute archival
stats = await client.consolidation.archive_expired_conversations(
    ttl_days=90,
    dry_run=False          # Perform actual archival
)
```

**Archival Strategy**:
- Old conversations moved to separate `:ArchivedConversation` node type
- Messages remain linked but marked with `archived_at` timestamp
- Searchable via separate archive query path
- Reduces active graph size while preserving audit trail

#### Message Linking

Messages auto-link sequentially when added (v0.2+):

```
(Conversation)-[:FIRST_MESSAGE]->(Message_1)     # O(1) access
(Message_1)-[:NEXT_MESSAGE]->(Message_2)
(Message_2)-[:NEXT_MESSAGE]->(Message_3)
```

For pre-v0.2 data without links:

```python
# Migrate legacy unlinked messages
result = await client.short_term.migrate_message_links()
# Returns: {conv_id_1: 15, conv_id_2: 23, ...} - messages linked per conversation
```

### Entity Deduplication and Resolution

#### Automatic Deduplication on Ingest

When `add_entity()` is called with `deduplicate=True` (default):

```python
entity, dedup_result = await client.long_term.add_entity(
    name="Jon Smith",
    entity_type="PERSON",
    deduplicate=True       # Trigger deduplication check
)

# Three possible outcomes:
if dedup_result.action == "created":
    # New entity, no matches
    pass
elif dedup_result.action == "merged":
    # Auto-merged with existing (confidence >= auto_merge_threshold)
    # entity is the existing entity
    # New name added as alias/mention
    print(f"Merged with {dedup_result.matched_entity_name}")
elif dedup_result.action == "flagged":
    # Flagged for review (between flag_threshold and auto_merge_threshold)
    # entity is newly created
    # SAME_AS relationship created with status="pending"
    print(f"Flagged as potential duplicate of {dedup_result.matched_entity_name}")
```

**Deduplication Thresholds** (configurable):

```python
from neo4j_agent_memory.memory.long_term import DeduplicationConfig

config = DeduplicationConfig(
    enabled=True,
    auto_merge_threshold=0.95,     # Auto-merge if >= 95% confidence
    flag_threshold=0.85,           # Flag for review if >= 85% confidence
    use_fuzzy_matching=True,       # Also check Levenshtein distance
    fuzzy_threshold=0.9,           # Fuzzy match threshold
    max_candidates=10,             # Max candidates to check
    match_same_type_only=True,     # PERSON != LOCATION even if same name
)

long_term = LongTermMemory(
    client=neo4j_client,
    embedder=embedder,
    deduplication=config
)
```

#### Manual Duplicate Review

```python
# Find all pending duplicates
duplicates = await client.long_term.find_potential_duplicates(
    limit=100
) -> list[tuple[Entity, Entity, float]]  # (source, target, confidence)

# Review each pair
for source, target, confidence in duplicates:
    print(f"{source.name} ~ {target.name} ({confidence:.1%})")
    
    # Review and decide
    await client.long_term.review_duplicate(
        source_id=source.id,
        target_id=target.id,
        confirm=True  # Merge them
        # confirm=False  # Reject and remove SAME_AS
    )

# Get all entities in a deduplication cluster
cluster = await client.long_term.get_same_as_cluster(entity_id)
# Returns all entities transitively linked via SAME_AS

# Consolidation job to auto-resolve duplicates
stats = await client.consolidation.dedupe_entities(
    min_confidence=0.90,
    dry_run=True  # Show what would be merged
) -> dict      # {reviewed, merged, clusters_consolidated}
```

### Reasoning Trace Consolidation

#### Long Trace Summarization

For traces with many steps, optionally summarize:

```python
# Find traces with excessive steps
stats = await client.consolidation.summarize_long_traces(
    max_steps_threshold=50,  # Flag traces with > 50 steps
    dry_run=True
) -> dict                    # {flagged_count, summarized_count}

# Summarize (uses LLM if configured)
stats = await client.consolidation.summarize_long_traces(
    max_steps_threshold=50,
    dry_run=False            # Perform summarization
)
```

#### Trace Outcome Recording (v0.2+)

Detailed trace completion with metrics:

```python
from neo4j_agent_memory import TraceOutcome

await client.reasoning.complete_trace(
    trace_id="trace-123",
    outcome=TraceOutcome(
        summary="Successfully found restaurant matching preferences",
        success=True,
        error_kind=None,              # or "validation_error", "tool_error", etc.
        related_entities=[
            EntityRef(entity_id="entity-1", relation="RECOMMENDED"),
            EntityRef(entity_id="entity-2", relation="REJECTED")
        ],
        metrics={
            "tools_used": 3,
            "api_calls": 5,
            "latency_ms": 2341
        }
    )
)
```

### Preference Consolidation

#### Superseded Preference Detection

Detect and mark preferences that are overridden by newer ones:

```python
# Find preferences that should be superseded
stats = await client.consolidation.detect_superseded_preferences(
    similarity_threshold=0.85,
    dry_run=True               # Show what would be marked
) -> dict                      # {detected_count, superseded_count}

# Mark as superseded
stats = await client.consolidation.detect_superseded_preferences(
    similarity_threshold=0.85,
    dry_run=False              # Mark superseded preferences
)
```

**Superseded Preference Lifecycle**:
1. New preference created: "I love Italian food"
2. Later, user states: "Actually, I prefer French cuisine"
3. Job detects semantic similarity
4. Old preference marked with `superseded_by` relationship
5. Query filters to only active preferences (no `superseded_by`)

### Buffered Write Queue (Fire-and-Forget)

For high-throughput scenarios, decouple write response from Neo4j round-trip:

```python
from neo4j_agent_memory import MemorySettings

settings = MemorySettings(
    memory=MemoryConfig(
        write_mode="buffered",     # Enable buffered writes
        max_pending=1000           # Queue size limit
    )
)

async with MemoryClient(settings) as client:
    # Returns immediately, queued for write
    await client.short_term.add_message(...)
    
    # Drain queue at shutdown (or explicitly)
    await client.flush()
    
    # Check for errors from background writes
    if client.write_errors:
        for error in client.write_errors:
            print(f"Write error: {error}")
```

**Queue Behavior**:
- Write calls return immediately
- Queued to bounded queue (max_pending)
- Background drain thread writes to Neo4j
- Errors recorded in `client.write_errors`
- `flush()` blocks until queue empty

### Vector Index Maintenance

#### Embedding Dimension Validation

Embeddings must match vector index dimensions:

```python
# Validate on connect (automatic)
await client.schema.validate_vector_index_dimensions(
    expected_dimensions=1536  # e.g., OpenAI text-embedding-3-small
)
# Raises: EmbeddingDimensionMismatchError if mismatch detected

# Manual check
is_valid = await client.schema.validate_vector_indexes()
```

#### Re-embedding Entities

If embedder changes (e.g., switch from OpenAI to Vertex AI):

```python
# Batch re-embed entities
result = await client.long_term.generate_embeddings_batch(
    entity_ids=["entity-1", "entity-2", ...],
    batch_size=32,
    on_progress=lambda done, total: print(f"{done}/{total}")
) -> EmbeddingResult              # {succeeded, failed, duration_ms}
```

---

## Detailed Schema and Indexes

### Complete Neo4j Schema

#### Node Properties

**Conversation Node**:
```cypher
(:Conversation {
    id: UUID,                       # Primary key
    session_id: String,             # User session identifier (indexed)
    title: String,                  # Optional conversation title
    created_at: DateTime,           # Creation timestamp
    updated_at: DateTime,           # Last update timestamp
    archived_at: DateTime | NULL,   # Archival timestamp (if archived)
    summary: String | NULL          # Optional conversation summary
})
```

**Message Node**:
```cypher
(:Message {
    id: UUID,                       # Primary key
    session_id: String,             # Session reference (for fast lookup)
    role: String,                   # "user", "assistant", "system"
    content: String,                # Message text
    metadata: String,               # JSON serialized metadata
    embedding: [Float],             # Vector (1536 dims for text-embedding-3-small)
    created_at: DateTime,
    
    # Optional fields for tool extraction
    entities_extracted: Integer,    # Count of entities extracted
    relations_extracted: Integer    # Count of relationships extracted
})
```

**Entity Node with Dynamic Labels**:
```cypher
(:Entity:Person:Individual {
    id: UUID,
    name: String,                   # (indexed)
    type: String,                   # "PERSON" (always set)
    subtype: String,                # "INDIVIDUAL" (optional)
    description: String | NULL,
    metadata: String,               # JSON dict
    embedding: [Float],             # Vector index
    confidence: Float,              # 0.0-1.0
    created_at: DateTime,
    updated_at: DateTime,
    
    # Enrichment (from Wikipedia/Diffbot)
    enriched_description: String | NULL,
    wikipedia_url: String | NULL,
    wikidata_id: String | NULL,
    image_url: String | NULL,
    enriched_at: DateTime | NULL,
    
    # For LOCATION entities only
    location: Point | NULL          # Neo4j Point(latitude, longitude)
})

(:Entity:Object:Vehicle {...})      # Another example label set
(:Entity:Location:Address {...})    # Location-specific
(:Entity:Organization:Company {...})# Organization-specific
```

**Preference Node**:
```cypher
(:Preference {
    id: UUID,
    user_id: String | NULL,         # For multi-tenant
    category: String,               # "food", "music", "travel", etc. (indexed)
    preference: String,             # The preference statement
    confidence: Float,              # 0.0-1.0
    metadata: String,               # JSON
    created_at: DateTime,
    updated_at: DateTime,
    superseded_by: String | NULL    # UUID of newer preference (if superseded)
})
```

**Fact Node**:
```cypher
(:Fact {
    id: UUID,
    subject: String,
    predicate: String,              # Relation type
    object: String,
    confidence: Float,
    created_at: DateTime
})
```

**ReasoningTrace Node**:
```cypher
(:ReasoningTrace {
    id: UUID,
    session_id: String,
    task: String,                   # The reasoning task
    started_at: DateTime,
    completed_at: DateTime | NULL,
    success: Boolean,
    outcome: String | NULL,         # Summary of result
    task_embedding: [Float],        # Vector index for search
    summary: String | NULL,         # Summarized steps (if many)
    
    # Metrics (v0.2+)
    step_count: Integer,
    tool_call_count: Integer,
    avg_step_duration_ms: Float,
    error_kind: String | NULL       # Type of error if failed
})
```

**ReasoningStep Node**:
```cypher
(:ReasoningStep {
    id: UUID,
    trace_id: String,
    thought: String,                # The reasoning
    action: String | NULL,          # What was done
    observation: String | NULL,     # What happened
    index: Integer,                 # Order in trace
    created_at: DateTime,
    embedding: [Float] | NULL       # Optional embedding
})
```

**ToolCall Node**:
```cypher
(:ToolCall {
    id: UUID,
    step_id: String,
    tool_name: String,              # "search_api", "send_email", etc.
    arguments: String,              # JSON serialized
    result: String,                 # JSON serialized
    status: String,                 # "SUCCESS", "ERROR", "PARTIAL"
    error: String | NULL,
    execution_time_ms: Float | NULL,
    created_at: DateTime
})
```

**Tool Node** (Reference):
```cypher
(:Tool {
    name: String,                   # "search_api" (unique, indexed)
    description: String | NULL,
    category: String | NULL         # "search", "communication", etc.
})
```

**Extractor Node** (Provenance):
```cypher
(:Extractor {
    id: UUID,
    name: String,                   # "GLiNEREntityExtractor" (indexed)
    version: String,
    config: String,                 # JSON serialized config
    created_at: DateTime
})
```

**Schema Node** (Schema versioning v0.2+):
```cypher
(:Schema {
    id: UUID,
    name: String,                   # "medical", "poleo", etc. (indexed)
    version: String,
    description: String | NULL,
    config: String,                 # JSON serialized EntitySchemaConfig
    is_active: Boolean,
    created_at: DateTime,
    created_by: String
})
```

**User Node** (Multi-tenant v0.2+):
```cypher
(:User {
    id: UUID,
    identifier: String,             # Username/email (unique, indexed)
    display_name: String | NULL,
    metadata: String | NULL,        # JSON dict
    created_at: DateTime
})
```

#### Relationship Properties

**RELATED_TO**:
```cypher
(Entity)-[:RELATED_TO {
    relation_type: String,          # "WORKS_AT", "KNOWS", "LOCATED_IN"
    confidence: Float,              # 0.0-1.0
    source: String | NULL,          # Where the relation came from
    created_at: DateTime
}]->(Entity)
```

**SAME_AS** (Deduplication):
```cypher
(Entity)-[:SAME_AS {
    confidence: Float,
    match_type: String,             # "embedding", "fuzzy", "exact"
    status: String,                 # "pending", "confirmed", "rejected"
    created_at: DateTime,
    reviewed_at: DateTime | NULL,
    reviewed_by: String | NULL
}]->(Entity)
```

**EXTRACTED_FROM** (Provenance):
```cypher
(Entity)-[:EXTRACTED_FROM {
    confidence: Float,
    start_pos: Integer,             # Character position in message
    end_pos: Integer,
    context: String | NULL,         # Surrounding text
    created_at: DateTime
}]->(Message)
```

**EXTRACTED_BY** (Provenance):
```cypher
(Entity)-[:EXTRACTED_BY {
    confidence: Float,
    extraction_time_ms: Float,
    created_at: DateTime
}]->(Extractor)
```

**MENTIONS**:
```cypher
(Message)-[:MENTIONS]->(Entity)    # No properties
```

**RELATED_TO (Tools)**:
```cypher
(ToolCall)-[:INSTANCE_OF]->(Tool)  # No properties
```

**TOUCHED** (Audit v0.2+):
```cypher
(ReasoningStep)-[:TOUCHED {
    relation: String                # "MODIFIED", "QUERIED", "CREATED"
}]->(Entity)
```

### Index and Constraint Definitions

```cypher
-- Unique constraints
CREATE CONSTRAINT conversation_id IF NOT EXISTS 
  ON (:Conversation) ASSERT conversation.id IS UNIQUE;

CREATE CONSTRAINT message_id IF NOT EXISTS 
  ON (:Message) ASSERT message.id IS UNIQUE;

CREATE CONSTRAINT entity_id IF NOT EXISTS 
  ON (:Entity) ASSERT entity.id IS UNIQUE;

CREATE CONSTRAINT preference_id IF NOT EXISTS 
  ON (:Preference) ASSERT preference.id IS UNIQUE;

CREATE CONSTRAINT fact_id IF NOT EXISTS 
  ON (:Fact) ASSERT fact.id IS UNIQUE;

CREATE CONSTRAINT trace_id IF NOT EXISTS 
  ON (:ReasoningTrace) ASSERT trace.id IS UNIQUE;

CREATE CONSTRAINT step_id IF NOT EXISTS 
  ON (:ReasoningStep) ASSERT step.id IS UNIQUE;

CREATE CONSTRAINT tool_call_id IF NOT EXISTS 
  ON (:ToolCall) ASSERT tool_call.id IS UNIQUE;

CREATE CONSTRAINT tool_name_unique IF NOT EXISTS 
  ON (:Tool) ASSERT tool.name IS UNIQUE;

CREATE CONSTRAINT user_identifier_unique IF NOT EXISTS 
  ON (:User) ASSERT user.identifier IS UNIQUE;

-- Text indexes
CREATE INDEX conversation_session_id IF NOT EXISTS 
  ON (:Conversation) (session_id);

CREATE INDEX message_session_id IF NOT EXISTS 
  ON (:Message) (session_id);

CREATE INDEX entity_name_index IF NOT EXISTS 
  ON (:Entity) (name);

CREATE INDEX entity_type_index IF NOT EXISTS 
  ON (:Entity) (type);

CREATE INDEX preference_category_index IF NOT EXISTS 
  ON (:Preference) (category);

CREATE INDEX extractor_name_index IF NOT EXISTS 
  ON (:Extractor) (name);

CREATE INDEX tool_name_index IF NOT EXISTS 
  ON (:Tool) (name);

CREATE INDEX schema_name_index IF NOT EXISTS 
  ON (:Schema) (name);

CREATE INDEX user_identifier_index IF NOT EXISTS 
  ON (:User) (identifier);

-- Vector indexes (1536 dims for OpenAI text-embedding-3-small)
CREATE VECTOR INDEX message_embedding_vector IF NOT EXISTS 
  FOR (m:Message) ON (m.embedding) 
  OPTIONS {indexType: 'vector', dimensions: 1536, similarity_metric: 'cosine'};

CREATE VECTOR INDEX entity_embedding_vector IF NOT EXISTS 
  FOR (e:Entity) ON (e.embedding) 
  OPTIONS {indexType: 'vector', dimensions: 1536, similarity_metric: 'cosine'};

CREATE VECTOR INDEX trace_embedding_vector IF NOT EXISTS 
  FOR (t:ReasoningTrace) ON (t.task_embedding) 
  OPTIONS {indexType: 'vector', dimensions: 1536, similarity_metric: 'cosine'};

-- Geospatial index (for Location entities with coordinates)
CREATE POINT INDEX location_spatial IF NOT EXISTS 
  FOR (e:Entity) ON (e.location);
```

---

## LLM Interactions for Memory

### 1. Conversation Summarization

**When**: LLM provider configured, conversation reaches threshold, or explicitly requested.

**Process**:

```python
# Explicit summarization request
summary = await client.short_term.get_conversation_summary(
    session_id="user-123",
    max_messages=None  # Use all messages, or cap for cost
) -> ConversationSummary

# Returns:
# {
#   text: "User asked for Italian restaurants, preferred casual dining...",
#   key_entities: [Entity(...), Entity(...)],
#   generated_at: datetime,
#   message_count: 42,
#   duration_ms: 1245
# }
```

**LLM Prompt Template**:

```
You are a conversation summarizer. Summarize the following conversation concisely (2-3 sentences).

Conversation:
{messages}

Summary:
```

**Implementation** (`src/neo4j_agent_memory/memory/short_term.py`):

```python
async def _llm_summarizer(transcript: str) -> str:
    """Summarize using OpenAI API (if configured)."""
    from openai import AsyncOpenAI
    
    client = AsyncOpenAI()
    response = await client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[
            {
                "role": "system",
                "content": "You are a conversation summarizer. Provide a 2-3 sentence summary."
            },
            {
                "role": "user",
                "content": f"Summarize:\n{transcript}"
            }
        ],
        temperature=0.3
    )
    return response.choices[0].message.content
```

**Cost Optimization**:
- Only summarize on-demand (lazy, not auto-triggered)
- Cache summaries in `Conversation.summary` property
- Option to cap message count when requesting summary

### 2. Entity Extraction with LLM

**When**: `extract_entities=True` on message add, or pipeline includes LLM stage.

**Extraction Pipeline** (Multi-stage fallback):

```
Input Text
    │
    ├─► Stage 1: spaCy NER (fast, local)
    │   └─ Extract high-confidence entities
    │
    ├─► Stage 2: GLiNER (zero-shot, local)
    │   └─ Extract lower-confidence entities
    │
    └─► Stage 3: LLM Fallback (expensive, remote)
        └─ Extract very difficult entities & relationships
```

**Configuration**:

```python
from neo4j_agent_memory import ExtractionConfig, ExtractorType

settings = MemorySettings(
    extraction=ExtractionConfig(
        extractor_type=ExtractorType.PIPELINE,
        enable_spacy=True,
        enable_gliner=True,
        enable_llm_fallback=True,      # Enable LLM extraction
        gliner_schema="poleo"
    ),
    llm=LLMConfig(
        provider=LLMProvider.OPENAI,
        model="gpt-4o-mini"
    )
)
```

**LLM Prompt** (from `extraction/llm_extractor.py`):

```
You are an entity extraction expert. Extract all entities and relationships from the text.

ENTITY TYPES:
- PERSON: Individual human beings
- ORGANIZATION: Companies, agencies, non-profits
- LOCATION: Geographic places, addresses
- OBJECT: Physical or digital items
- EVENT: Incidents, meetings, transactions

OUTPUT FORMAT (JSON):
{
  "entities": [
    {"name": "...", "type": "PERSON", "confidence": 0.95},
    {"name": "...", "type": "ORGANIZATION", "confidence": 0.90}
  ],
  "relationships": [
    {"source": "...", "target": "...", "type": "WORKS_AT", "confidence": 0.88}
  ]
}

TEXT: {text}

ENTITIES AND RELATIONSHIPS:
```

**Example Flow**:

```
Message: "John Smith from Acme Corp met Jane Doe at the San Francisco office on Monday."

1. spaCy extracts: [John Smith (PERSON), Acme Corp (ORG), San Francisco (LOC), Jane Doe (PERSON)]
2. GLiNER adds: [Acme Corp (ORG), San Francisco (LOC)] with lower confidence
3. LLM adds: "office" (LOCATION), relationships: John-WORKS_AT-Acme, John-KNOWS-Jane, office-LOCATED_IN-San Francisco
```

### 3. Relationship Extraction

**GLiREL** (GPU-accelerated, no LLM needed):

```python
from neo4j_agent_memory.extraction import GLiRELExtractor, GLiNERWithRelationsExtractor

# Combined entity + relationship extraction
extractor = GLiNERWithRelationsExtractor.for_poleo()
result = await extractor.extract("John works at Acme Corp in NYC.")

# Returns:
# ExtractionResult {
#   entities: [John (PERSON), Acme Corp (ORG), NYC (LOCATION)],
#   relationships: [
#     {source: "John", target: "Acme Corp", type: "WORKS_AT", confidence: 0.92},
#     {source: "Acme Corp", target: "NYC", type: "LOCATED_IN", confidence: 0.88}
#   ]
# }
```

**Default Relation Types** (POLE+O model):

```python
DEFAULT_RELATION_TYPES = {
    "WORKS_AT": "Person employed by or working at an organization",
    "LIVES_IN": "Person lives in a location",
    "MEMBER_OF": "Person is member of an organization",
    "KNOWS": "Person knows another person",
    "LOCATED_IN": "Entity is located in a location",
    "FOUNDED_BY": "Organization founded by a person",
    "OWNS": "Person or organization owns an object",
    "PART_OF": "Object is part of another object",
    "USES": "Person or organization uses an object/tool",
    # ... more types
}
```

### 4. Preference Detection

**Pattern-Based** (no LLM, zero latency):

When a message is stored, preference detector scans for patterns:

```python
from neo4j_agent_memory.mcp._preference_detector import PreferenceDetector

detector = PreferenceDetector()

# Detects patterns like:
preferences = detector.detect("I love Italian food and prefer outdoor dining")
# Returns: [
#   Preference(category="food", text="loves Italian food", confidence=0.95),
#   Preference(category="dining", text="prefers outdoor dining", confidence=0.90)
# ]
```

**Regex Patterns** (example):

```python
PREFERENCE_PATTERNS = {
    "love": r"i (?:love|adore|really enjoy) ([^,.!?]+)",
    "prefer": r"i (?:prefer|would rather|like) ([^,.!?]+)",
    "hate": r"i (?:hate|dislike|can't stand) ([^,.!?]+)",
    "avoid": r"i (?:avoid|don't eat|won't) ([^,.!?]+)",
}
```

**Async Background Processing**:

```python
# When auto_preferences=True on MemoryIntegration:
async with MemoryIntegration(..., auto_preferences=True) as memory:
    # store_message() triggers background preference detection
    await memory.store_message("user", "I love sushi and prefer spicy food")
    # Background task fires: detect() -> add_preference() calls
```

**Flow**:

```
store_message()
    │
    ├─ (Immediate) Create Message node, extract entities
    │
    └─ (Background task, asyncio.create_task)
       │
       ├─ PreferenceDetector.detect(message.content)
       │  └─ Run regex patterns
       │
       └─ For each detected preference:
          └─ client.long_term.add_preference(category, text, confidence)
```

### 5. Entity Enrichment

**Wikimedia (Free, Rate-Limited)**:

```python
from neo4j_agent_memory.enrichment import WikimediaProvider

provider = WikimediaProvider(rate_limit=0.5)  # 0.5s between requests
result = await provider.enrich("Albert Einstein", "PERSON")

# Returns: EnrichmentResult {
#   status: EnrichmentStatus.SUCCESS,
#   description: "German-born theoretical physicist...",
#   wikipedia_url: "https://en.wikipedia.org/wiki/Albert_Einstein",
#   wikidata_id: "Q937",
#   image_url: "https://commons.wikimedia.org/wiki/Special:FilePath/Einstein_1921.jpg"
# }
```

**Diffbot** (Requires API key, richer data):

```python
from neo4j_agent_memory.enrichment import DiffbotProvider

provider = DiffbotProvider(api_key="...")
result = await provider.enrich("Apple Inc", "ORGANIZATION")

# Returns enriched organization data:
# {
#   description, wikipedia_url, image_url, wikidata_id,
#   related_entities: ["Tim Cook", "Steve Jobs", ...],
#   categories: ["Technology", "Software", ...],
#   founded_date, headquarters, stock_symbol, ...
# }
```

**Background Processing**:

```python
from neo4j_agent_memory import EnrichmentConfig, EnrichmentProvider

settings = MemorySettings(
    enrichment=EnrichmentConfig(
        enabled=True,
        providers=[EnrichmentProvider.WIKIMEDIA],
        background_enabled=True,  # Non-blocking
        min_confidence=0.7,        # Only enrich high-confidence entities
        entity_types=["PERSON", "ORGANIZATION"]
    )
)

# When entity added:
entity, _ = await client.long_term.add_entity("Albert Einstein", "PERSON")
# Returns immediately - entity stored

# Background enrichment service:
# 1. Detects entity meets criteria (type, confidence)
# 2. Fetches from Wikipedia in background
# 3. Updates entity with enriched_description, wikipedia_url, image_url
# 4. User queries see enriched data on next read
```

**Enrichment Storage**:

```cypher
(Entity {
    id: "entity-123",
    name: "Albert Einstein",
    enriched_description: "German-born theoretical physicist, developed theory of relativity",
    wikipedia_url: "https://en.wikipedia.org/wiki/Albert_Einstein",
    wikidata_id: "Q937",
    image_url: "https://commons.wikimedia.org/...",
    enriched_at: datetime(2026-05-25T14:32:00Z)
})
```

### 6. LLM-Based Trace Summarization

For long reasoning traces (50+ steps), optionally summarize:

```python
# Consolidation job
stats = await client.consolidation.summarize_long_traces(
    max_steps_threshold=50,
    dry_run=False  # Execute summarization
)
```

**Prompt**:

```
Summarize the following reasoning trace concisely (2-3 sentences).

Trace Task: {task}
Steps:
{step_summaries}

Summary:
```

**Implementation**:

```python
async def _summarize_trace_steps(steps: list[ReasoningStep]) -> str:
    """Use LLM to summarize many steps."""
    summary_lines = [
        f"{step.index}. {step.thought}"
        for step in steps[:20]  # First 20 steps
    ]
    if len(steps) > 20:
        summary_lines.append(f"... and {len(steps) - 20} more steps")
    
    response = await llm.create({
        "role": "system", "content": "You are a reasoning summarizer.",
        "role": "user", "content": f"Summarize:\n" + "\n".join(summary_lines)
    })
    
    return response.content
```

### 7. LLM Observational Memory

**MemoryObserver** (v0.4+) - generates inline reflections:

```python
from neo4j_agent_memory.mcp._observer import MemoryObserver

observer = MemoryObserver(
    client=memory_client,
    threshold_tokens=30000  # Generate reflection when conversation > 30k tokens
)

# Automatically called by MCP server during get_observations
observations = await observer.get_observations(
    session_id="user-123"
) -> dict {
    "reflections": ["User prefers DIY solutions over hiring", "Focuses on cost efficiency"],
    "observations": [...],
    "session_stats": {...}
}
```

**Reflection Generation**:

When token count exceeded, LLM scans older messages (sliding window):

```python
# Pseudo-code for reflection generation
async def generate_reflections(messages: list[Message]) -> list[str]:
    """Generate key insights about user from older messages."""
    older_messages = [
        m for m in messages 
        if (datetime.now() - m.created_at).days > 7
    ]
    
    if not older_messages:
        return []
    
    prompt = f"""Based on these conversation excerpts, what are 3-5 key insights about the user?

Excerpts:
{format_message_excerpts(older_messages)}

Key insights (brief, 1-2 words each):
"""
    
    response = await llm.complete(prompt)
    return response.strip().split("\n")
```

---

## Summary: API Surface by Layer

### Top Level: MemoryClient
- `connect()`, `close()`, `flush()`
- `short_term`, `long_term`, `reasoning`, `query` properties
- `get_context()`, `get_stats()`, `get_graph()`, `get_locations()`

### Short-Term Memory: Conversations & Messages
- `add_message()`, `add_messages_batch()`
- `get_conversation()`, `search_messages()`, `get_context()`
- `list_sessions()`, `clear_session()`, `delete_message()`
- `get_conversation_summary()`, `migrate_message_links()`

### Long-Term Memory: Entities & Knowledge
- `add_entity()`, `search_entities()`, `get_entity()`
- `add_preference()`, `search_preferences()`, `get_preferences()`, `delete_preference()`
- `add_fact()`, `search_facts()`
- `create_relationship()`, `link_entity_to_message()`
- `geocode_locations()`, `search_locations_near()`, `search_locations_in_bounding_box()`
- `add_enrichment()`, `get_entity_provenance()`, `register_extractor()`

### Reasoning Memory: Decision Audit Trails
- `start_trace()`, `record_step()`, `record_tool_call()`, `complete_trace()`
- `get_trace()`, `search_traces()`, `get_context()`, `get_stats()`
- `streaming_trace_recorder()` context manager

### Consolidation (Maintenance)
- `dedupe_entities()` - merge duplicates
- `summarize_long_traces()` - compress multi-step traces
- `detect_superseded_preferences()` - mark old preferences
- `archive_expired_conversations()` - TTL-based archival

### MCP Tools (6 Core + 12 Extended + 4 Platinum)
- Core: `memory_search`, `memory_get_context`, `memory_store_message`, `memory_add_entity`, `memory_add_preference`, `memory_add_fact`
- Extended: `memory_get_conversation`, `memory_list_sessions`, `memory_get_entity`, `memory_export_graph`, `memory_create_relationship`, `memory_start_trace`, `memory_record_step`, `memory_complete_trace`, `memory_get_observations`, `graph_query`
- Platinum (NAMS): `memory_set_entity_feedback`, `memory_get_entity_history`, `memory_get_entity_provenance`, `memory_get_reflections`

---

## Document Versioning

- **v1.0** (2026-05-25): Initial comprehensive design document covering 0.4.0 release with bolt and NAMS backends, 18 MCP tools, full extraction pipeline, entity deduplication, and reasoning traces.
- **v1.1** (2026-05-25): Added memory maintenance (consolidation, archival, deduplication), complete Neo4j schema with all properties and indexes, and detailed LLM interactions (summarization, extraction, enrichment, preference detection, observations).
