package state

// migrations is the canonical, forward-only list of schema versions.
//
// Rules:
//   - Never edit a shipped migration — add a new one at the end.
//   - This task (T6) owns only the core observation/session/memory/graph schema.
//   - Search-index tables are added by Task 14 (BM25) and Task 15 (vector).
//   - Each migration must be idempotent-safe under the version guard in
//     Migrate (we don't re-run executed versions, but transactions are still
//     used so partial application can't corrupt the DB).
//
// The schema favours explicit JSON-as-TEXT columns over normalised side tables
// for evolving structures (e.g. tool_input, facts). PostgreSQL handles JSON via
// json_extract_path; later we can promote frequent fields to columns if needed.
var migrations = []migration{
	{
		version: 1,
		name:    "core_tables",
		sql: `
-- Sessions: one row per agent conversation lifecycle.
CREATE TABLE sessions (
    id                TEXT PRIMARY KEY,           -- sess-...
    project           TEXT NOT NULL DEFAULT '',
    cwd               TEXT,
    status            TEXT NOT NULL DEFAULT 'active', -- active | completed | abandoned
    observation_count INTEGER NOT NULL DEFAULT 0,
    tags_json         TEXT NOT NULL DEFAULT '[]',  -- JSON array
    summary           TEXT,
    started_at        TEXT NOT NULL,              -- ISO-8601 UTC
    updated_at        TEXT NOT NULL,
    ended_at          TEXT
);
CREATE INDEX idx_sessions_project ON sessions(project);
CREATE INDEX idx_sessions_status  ON sessions(status);

-- Observations: every RawObservation/CompressedObservation lives here.
-- compressed=0 → row is a RawObservation (raw_json populated)
-- compressed=1 → row is a CompressedObservation (title/facts/... populated)
CREATE TABLE observations (
    id                  TEXT PRIMARY KEY,         -- obs-...
    session_id          TEXT NOT NULL,
    project             TEXT NOT NULL DEFAULT '',
    hook_type           TEXT NOT NULL,
    tool_name           TEXT,
    tool_input_json     TEXT,                     -- JSON object
    tool_output         TEXT,
    user_prompt         TEXT,
    raw_json            TEXT NOT NULL DEFAULT '{}', -- full filtered payload
    modality            TEXT NOT NULL DEFAULT 'text', -- text | image | mixed
    image_ref           TEXT,                     -- blob path / URL for image data
    has_secrets         INTEGER NOT NULL DEFAULT 0, -- bool (FR-9 flag)
    compressed          INTEGER NOT NULL DEFAULT 0,
    type                TEXT,                     -- e.g. file_read
    title               TEXT,
    subtitle            TEXT,
    facts_json          TEXT,                     -- JSON array of strings
    narrative           TEXT,
    concepts_json       TEXT,                     -- JSON array
    files_json          TEXT,                     -- JSON array
    importance          INTEGER,
    confidence          REAL,
    image_description   TEXT,
    created_at          TEXT NOT NULL,
    compressed_at       TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
CREATE INDEX idx_observations_session    ON observations(session_id);
CREATE INDEX idx_observations_project    ON observations(project);
CREATE INDEX idx_observations_hook_type  ON observations(hook_type);
CREATE INDEX idx_observations_created_at ON observations(created_at);
CREATE INDEX idx_observations_compressed ON observations(compressed);

-- Memories: distilled facts produced by the consolidation pipeline (Task 22+).
CREATE TABLE memories (
    id                       TEXT PRIMARY KEY,    -- mem-...
    project                  TEXT NOT NULL DEFAULT '',
    type                     TEXT NOT NULL,       -- pattern|preference|architecture|bug|workflow|fact
    tier                     TEXT NOT NULL DEFAULT 'semantic', -- working|episodic|semantic|procedural
    title                    TEXT NOT NULL,
    content                  TEXT NOT NULL,
    concepts_json            TEXT NOT NULL DEFAULT '[]',
    files_json               TEXT NOT NULL DEFAULT '[]',
    session_ids_json         TEXT NOT NULL DEFAULT '[]',
    source_observation_ids   TEXT NOT NULL DEFAULT '[]', -- JSON array
    strength                 REAL NOT NULL DEFAULT 1.0,
    is_latest                INTEGER NOT NULL DEFAULT 1,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    last_retrieved_at        TEXT
);
CREATE INDEX idx_memories_project   ON memories(project);
CREATE INDEX idx_memories_type      ON memories(type);
CREATE INDEX idx_memories_tier      ON memories(tier);
CREATE INDEX idx_memories_strength  ON memories(strength);
CREATE INDEX idx_memories_is_latest ON memories(is_latest);

-- Session summaries: one row per consolidated session.
CREATE TABLE sessions_summary (
    session_id        TEXT PRIMARY KEY,
    title             TEXT NOT NULL,
    narrative         TEXT NOT NULL,
    key_decisions     TEXT NOT NULL DEFAULT '[]', -- JSON array
    files_modified    TEXT NOT NULL DEFAULT '[]', -- JSON array
    concepts          TEXT NOT NULL DEFAULT '[]', -- JSON array
    created_at        TEXT NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

-- Graph: entity nodes (POLE+O typed).
CREATE TABLE graph_entities (
    id                TEXT PRIMARY KEY,           -- ent-...
    project           TEXT NOT NULL DEFAULT '',
    name              TEXT NOT NULL,
    type              TEXT NOT NULL,              -- PERSON | OBJECT | LOCATION | EVENT | ORGANIZATION
    subtype           TEXT,
    confidence        REAL NOT NULL DEFAULT 0.5,
    aliases_json      TEXT NOT NULL DEFAULT '[]',
    enriched_json     TEXT,                       -- {wikipedia_url, wikidata_id, image_url, description}
    is_sensitive      INTEGER NOT NULL DEFAULT 0, -- FR-9 — skip enrichment for these
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    UNIQUE (project, name, type)
);
CREATE INDEX idx_graph_entities_project ON graph_entities(project);
CREATE INDEX idx_graph_entities_name    ON graph_entities(name);
CREATE INDEX idx_graph_entities_type    ON graph_entities(type);

-- Graph: typed, directed edges with confidence + provenance.
CREATE TABLE graph_edges (
    id                TEXT PRIMARY KEY,           -- edg-...
    project           TEXT NOT NULL DEFAULT '',
    source_id         TEXT NOT NULL,
    target_id         TEXT NOT NULL,
    type              TEXT NOT NULL,              -- DEPENDS_ON | IMPLEMENTS | WORKS_AT | ...
    confidence        REAL NOT NULL DEFAULT 0.5,
    source_observation_id TEXT,
    created_at        TEXT NOT NULL,
    FOREIGN KEY (source_id) REFERENCES graph_entities(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES graph_entities(id) ON DELETE CASCADE
);
CREATE INDEX idx_graph_edges_source  ON graph_edges(source_id);
CREATE INDEX idx_graph_edges_target  ON graph_edges(target_id);
CREATE INDEX idx_graph_edges_type    ON graph_edges(type);
CREATE INDEX idx_graph_edges_project ON graph_edges(project);

-- Audit log: every share, delete, and team mutation lands here (FR-39).
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TEXT NOT NULL,
    actor           TEXT NOT NULL,                -- user id or agent name
    action          TEXT NOT NULL,                -- delete | share | revoke | redact
    target_type     TEXT NOT NULL,                -- observation | memory | entity
    target_id       TEXT NOT NULL,
    payload_json    TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_log_timestamp ON audit_log(timestamp);
CREATE INDEX idx_audit_log_target    ON audit_log(target_id);

-- Generic KV store backing the 34 documented scopes (kv.go).
-- Keys are namespaced strings (e.g. "sessions:myproject:sess-abc"); values are JSON blobs.
CREATE TABLE kv (
    scope        TEXT NOT NULL,
    key          TEXT NOT NULL,
    value_json   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (scope, key)
);
CREATE INDEX idx_kv_scope ON kv(scope);

-- Dedup window persistence (Task 9 may opt in for restart durability).
CREATE TABLE dedup_window (
    session_id   TEXT NOT NULL,
    hash         TEXT NOT NULL,
    seen_at      TEXT NOT NULL,
    PRIMARY KEY (session_id, hash)
);
CREATE INDEX idx_dedup_seen_at ON dedup_window(seen_at);
`,
	},
	{
		version: 2,
		name:    "files_gin_index",
		sql: `
-- GIN index on files_json for fast file-history lookups (G02).
-- files_json is stored as TEXT containing a JSON array; casting to jsonb at
-- index time lets us use the @> containment operator efficiently.
CREATE INDEX IF NOT EXISTS idx_observations_files_gin
    ON observations USING GIN (CAST(files_json AS jsonb));
`,
	},
	{
		version: 3,
		name:    "patterns_and_snapshots",
		sql: `
-- Detected recurring patterns across sessions (G04).
CREATE TABLE IF NOT EXISTS patterns (
    id          TEXT PRIMARY KEY,
    project     TEXT NOT NULL DEFAULT '',
    pattern     TEXT NOT NULL,
    count       INTEGER NOT NULL DEFAULT 1,
    examples    TEXT NOT NULL DEFAULT '[]', -- JSON array of {obsId, title}
    last_seen   TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_patterns_project   ON patterns(project);
CREATE INDEX IF NOT EXISTS idx_patterns_count     ON patterns(count DESC);

-- Snapshots: point-in-time export bundles (G06).
CREATE TABLE IF NOT EXISTS snapshots (
    id          TEXT PRIMARY KEY,
    project     TEXT NOT NULL DEFAULT '',
    bundle_json TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_snapshots_project ON snapshots(project);
`,
	},
	{
		version: 4,
		name:    "conversations_messages",
		sql: `
-- Short-term memory: conversations and messages (G08, mirroring DESIGN.md).
CREATE TABLE IF NOT EXISTS conversations (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    project     TEXT NOT NULL DEFAULT '',
    title       TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversations_session  ON conversations(session_id);
CREATE INDEX IF NOT EXISTS idx_conversations_project  ON conversations(project);

CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL,
    project         TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL,  -- user | assistant | system
    content         TEXT NOT NULL,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_session      ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_messages_created      ON messages(created_at);
`,
	},
	{
		version: 5,
		name:    "preferences_facts",
		sql: `
-- Preferences (G09).
CREATE TABLE IF NOT EXISTS preferences (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL DEFAULT '',
    category        TEXT NOT NULL,
    preference      TEXT NOT NULL,
    confidence      REAL NOT NULL DEFAULT 1.0,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    superseded_by   TEXT  -- id of newer preference that replaces this one
);
CREATE INDEX IF NOT EXISTS idx_preferences_project  ON preferences(project);
CREATE INDEX IF NOT EXISTS idx_preferences_category ON preferences(category);

-- Facts: subject–predicate–object triples (G10).
CREATE TABLE IF NOT EXISTS facts (
    id          TEXT PRIMARY KEY,
    project     TEXT NOT NULL DEFAULT '',
    subject     TEXT NOT NULL,
    predicate   TEXT NOT NULL,
    object_val  TEXT NOT NULL,
    confidence  REAL NOT NULL DEFAULT 1.0,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_facts_project   ON facts(project);
CREATE INDEX IF NOT EXISTS idx_facts_subject   ON facts(subject);
CREATE INDEX IF NOT EXISTS idx_facts_predicate ON facts(predicate);
`,
	},
	{
		version: 6,
		name:    "reasoning_traces",
		sql: `
-- Reasoning memory: traces, steps, tool calls (G11).
CREATE TABLE IF NOT EXISTS reasoning_traces (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    project      TEXT NOT NULL DEFAULT '',
    task         TEXT NOT NULL,
    started_at   TEXT NOT NULL,
    completed_at TEXT,
    success      INTEGER NOT NULL DEFAULT 0,
    outcome      TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_traces_session ON reasoning_traces(session_id);
CREATE INDEX IF NOT EXISTS idx_traces_project ON reasoning_traces(project);

CREATE TABLE IF NOT EXISTS reasoning_steps (
    id          TEXT PRIMARY KEY,
    trace_id    TEXT NOT NULL REFERENCES reasoning_traces(id) ON DELETE CASCADE,
    thought     TEXT NOT NULL,
    action      TEXT,
    observation TEXT,
    step_index  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_steps_trace ON reasoning_steps(trace_id);

CREATE TABLE IF NOT EXISTS tool_calls (
    id                 TEXT PRIMARY KEY,
    step_id            TEXT NOT NULL REFERENCES reasoning_steps(id) ON DELETE CASCADE,
    tool_name          TEXT NOT NULL,
    arguments_json     TEXT NOT NULL DEFAULT '{}',
    result_json        TEXT NOT NULL DEFAULT '{}',
    status             TEXT NOT NULL DEFAULT 'success', -- success | error | partial
    error_msg          TEXT,
    execution_time_ms  REAL,
    created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_step ON tool_calls(step_id);
`,
	},
	{
		version: 7,
		name:    "entity_dedup_geocode_users",
		sql: `
-- Entity deduplication: SAME_AS edges (G12).
CREATE TABLE IF NOT EXISTS entity_same_as (
    source_id    TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    confidence   REAL NOT NULL DEFAULT 0.0,
    match_type   TEXT NOT NULL DEFAULT 'unknown', -- exact | fuzzy | semantic
    status       TEXT NOT NULL DEFAULT 'pending', -- pending | confirmed | rejected
    created_at   TEXT NOT NULL,
    reviewed_at  TEXT,
    PRIMARY KEY (source_id, target_id)
);
CREATE INDEX IF NOT EXISTS idx_same_as_source ON entity_same_as(source_id);
CREATE INDEX IF NOT EXISTS idx_same_as_target ON entity_same_as(target_id);
CREATE INDEX IF NOT EXISTS idx_same_as_status ON entity_same_as(status);

-- Geocoding columns on graph_entities (G14).
ALTER TABLE graph_entities ADD COLUMN IF NOT EXISTS latitude  REAL;
ALTER TABLE graph_entities ADD COLUMN IF NOT EXISTS longitude REAL;
ALTER TABLE graph_entities ADD COLUMN IF NOT EXISTS geocoded_at TEXT;

-- Users table for multi-tenant support (G15).
CREATE TABLE IF NOT EXISTS users (
    id           TEXT PRIMARY KEY,
    identifier   TEXT NOT NULL UNIQUE, -- username / email
    display_name TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_identifier ON users(identifier);

-- user_id columns on key tables (G15) — nullable so existing rows are unaffected.
ALTER TABLE sessions      ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE observations  ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE memories      ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE preferences   ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE facts         ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE reasoning_traces ADD COLUMN IF NOT EXISTS user_id TEXT;
`,
	},
	{
		version: 8,
		name:    "orchestration",
		sql: `
-- Actions: DAG work items (G16).
CREATE TABLE IF NOT EXISTS actions (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending | in_progress | completed | failed
    priority        INTEGER NOT NULL DEFAULT 5,
    dependencies    TEXT NOT NULL DEFAULT '[]',      -- JSON array of action IDs
    result_json     TEXT,
    due_at          TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_actions_project ON actions(project);
CREATE INDEX IF NOT EXISTS idx_actions_status  ON actions(status);

-- Leases: multi-agent exclusive locks on actions (G17).
CREATE TABLE IF NOT EXISTS leases (
    id          TEXT PRIMARY KEY,
    action_id   TEXT NOT NULL REFERENCES actions(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_leases_action ON leases(action_id);

-- Routines: reusable workflow templates (G18).
CREATE TABLE IF NOT EXISTS routines (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    description     TEXT,
    template_json   TEXT NOT NULL DEFAULT '[]',  -- JSON array of action templates
    params_schema   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_routines_project ON routines(project);

-- Signals: inter-agent messages (G19).
CREATE TABLE IF NOT EXISTS signals (
    id          TEXT PRIMARY KEY,
    from_agent  TEXT NOT NULL,
    to_agent    TEXT NOT NULL,
    message     TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    sent_at     TEXT NOT NULL,
    read_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_signals_to_agent ON signals(to_agent);
CREATE INDEX IF NOT EXISTS idx_signals_sent_at  ON signals(sent_at);

-- Checkpoints: external condition gates (G20).
CREATE TABLE IF NOT EXISTS checkpoints (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL DEFAULT '',
    condition_expr  TEXT NOT NULL,
    satisfied_at    TEXT,
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checkpoints_project ON checkpoints(project);

-- Sentinels: event-driven watchers (G20).
CREATE TABLE IF NOT EXISTS sentinels (
    id              TEXT PRIMARY KEY,
    project         TEXT NOT NULL DEFAULT '',
    event_pattern   TEXT NOT NULL,
    handler_url     TEXT NOT NULL,
    triggered_at    TEXT,
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sentinels_project ON sentinels(project);
`,
	},
	{
		version: 9,
		name:    "collab_governance_slots_working",
		sql: `
-- Team sharing (G22).
CREATE TABLE IF NOT EXISTS team_shared (
    memory_id   TEXT NOT NULL,
    team_id     TEXT NOT NULL,
    shared_by   TEXT NOT NULL,
    mode        TEXT NOT NULL DEFAULT 'read', -- read | edit
    shared_at   TEXT NOT NULL,
    PRIMARY KEY (memory_id, team_id)
);
CREATE INDEX IF NOT EXISTS idx_team_shared_team ON team_shared(team_id);

CREATE TABLE IF NOT EXISTS team_feed (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL,
    memory_id   TEXT NOT NULL,
    shared_by   TEXT NOT NULL,
    shared_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_team_feed_team    ON team_feed(team_id);
CREATE INDEX IF NOT EXISTS idx_team_feed_shared  ON team_feed(shared_at);

-- Slots: pinned editable memory slots (G24).
CREATE TABLE IF NOT EXISTS slots (
    id          TEXT PRIMARY KEY,
    project     TEXT NOT NULL DEFAULT '',
    slot_name   TEXT NOT NULL,
    content     TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL,
    UNIQUE (project, slot_name)
);
CREATE INDEX IF NOT EXISTS idx_slots_project ON slots(project);

-- Working memory: session-scoped LIFO stack (G25).
CREATE TABLE IF NOT EXISTS working_memory (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    content     TEXT NOT NULL,
    pushed_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_working_session ON working_memory(session_id);
CREATE INDEX IF NOT EXISTS idx_working_pushed  ON working_memory(pushed_at);
`,
	},
	{
		version: 10,
		name:    "facets_lessons_skills",
		sql: `
-- Facets: dimension:value tagging on memories (G26).
CREATE TABLE IF NOT EXISTS memory_facets (
    memory_id   TEXT NOT NULL,
    dimension   TEXT NOT NULL,
    value       TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (memory_id, dimension, value)
);
CREATE INDEX IF NOT EXISTS idx_facets_memory    ON memory_facets(memory_id);
CREATE INDEX IF NOT EXISTS idx_facets_dimension ON memory_facets(dimension, value);

-- Lessons: extracted learning from sessions (G27).
CREATE TABLE IF NOT EXISTS lessons (
    id          TEXT PRIMARY KEY,
    project     TEXT NOT NULL DEFAULT '',
    session_id  TEXT NOT NULL,
    lesson      TEXT NOT NULL,
    context     TEXT,
    strength    REAL NOT NULL DEFAULT 1.0,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lessons_project ON lessons(project);
CREATE INDEX IF NOT EXISTS idx_lessons_session ON lessons(session_id);

-- Skills: acquired skills tracked across sessions (G28).
CREATE TABLE IF NOT EXISTS skills (
    id                  TEXT PRIMARY KEY,
    project             TEXT NOT NULL DEFAULT '',
    skill_name          TEXT NOT NULL,
    level               TEXT NOT NULL DEFAULT 'novice', -- novice | competent | expert
    evidence_count      INTEGER NOT NULL DEFAULT 1,
    last_demonstrated   TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    UNIQUE (project, skill_name)
);
CREATE INDEX IF NOT EXISTS idx_skills_project ON skills(project);
`,
	},
}
