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
// for evolving structures (e.g. tool_input, facts). SQLite handles JSON via
// json_extract; later we can promote frequent fields to columns if needed.
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
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
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
}
