package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned by KV.Get when the (scope, key) tuple has no value.
var ErrNotFound = errors.New("state: not found")

// Scope groups KV keys by purpose. The list is open-ended; the catalogue
// here mirrors the 34 documented scopes in agent_mem.md §"State Management"
// — add a constant when a new scope is introduced rather than passing magic strings.
type Scope string

const (
	ScopeSessions          Scope = "sessions"
	ScopeObservations      Scope = "observations"
	ScopeMemories          Scope = "memories"
	ScopeProjectMeta       Scope = "project_meta"
	ScopeProfile           Scope = "profile"
	ScopeTeam              Scope = "team"
	ScopeTeamShares        Scope = "team_shares"
	ScopeAudit             Scope = "audit"
	ScopeActions           Scope = "actions"
	ScopeLeases            Scope = "leases"
	ScopeRoutines          Scope = "routines"
	ScopeSignals           Scope = "signals"
	ScopeReasoningTraces   Scope = "reasoning_traces"
	ScopeSearchCache       Scope = "search_cache"
	ScopeEmbeddings        Scope = "embeddings"
	ScopeBM25Index         Scope = "bm25_index"
	ScopeGraphCache        Scope = "graph_cache"
	ScopeEnrichmentCache   Scope = "enrichment_cache"
	ScopeFrontier          Scope = "frontier"
	ScopeNextStep          Scope = "next_step"
	ScopeContextCache      Scope = "context_cache"
	ScopeFileHistory       Scope = "file_history"
	ScopeTimeline          Scope = "timeline"
	ScopeLessons           Scope = "lessons"
	ScopeMetrics           Scope = "metrics"
	ScopeFlags             Scope = "flags"
	ScopeProviders         Scope = "providers"
	ScopeUsers             Scope = "users"
	ScopeAuthTokens        Scope = "auth_tokens"
	ScopeRateLimits        Scope = "rate_limits"
	ScopeConsolidationLog  Scope = "consolidation_log"
	ScopeDecayLog          Scope = "decay_log"
	ScopeMigrationsApplied Scope = "migrations_applied"
	ScopeHealthChecks      Scope = "health_checks"
)

// KV is a typed key/value layer over the `kv` PostgreSQL table. Values are stored
// as JSON; callers pass any JSON-serialisable Go value.
type KV struct {
	db *sql.DB
}

// NewKV binds a KV to an open Store.
func NewKV(s *Store) *KV { return &KV{db: s.DB} }

// Key returns the canonical, namespaced storage key for a scope, project,
// and identifier. Project may be empty for global scopes.
func Key(scope Scope, project, id string) string {
	var b strings.Builder
	b.Grow(len(scope) + len(project) + len(id) + 2)
	b.WriteString(string(scope))
	if project != "" {
		b.WriteByte(':')
		b.WriteString(project)
	}
	if id != "" {
		b.WriteByte(':')
		b.WriteString(id)
	}
	return b.String()
}

// Put writes v as JSON under (scope, key). Replaces any existing value.
func (kv *KV) Put(ctx context.Context, scope Scope, key string, v any) error {
	if scope == "" || key == "" {
		return errors.New("state.KV.Put: scope and key required")
	}
	blob, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("state.KV.Put: marshal: %w", err)
	}
	_, err = kv.db.ExecContext(ctx, `
        INSERT INTO kv (scope, key, value_json, updated_at)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT(scope, key) DO UPDATE SET
            value_json = excluded.value_json,
            updated_at = excluded.updated_at
    `, string(scope), key, string(blob), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// Get reads (scope, key) into v. Returns ErrNotFound if no row exists.
func (kv *KV) Get(ctx context.Context, scope Scope, key string, v any) error {
	if scope == "" || key == "" {
		return errors.New("state.KV.Get: scope and key required")
	}
	var blob string
	err := kv.db.QueryRowContext(ctx,
		`SELECT value_json FROM kv WHERE scope = $1 AND key = $2`,
		string(scope), key,
	).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(blob), v)
}

// Delete removes (scope, key). It is not an error if the key didn't exist.
func (kv *KV) Delete(ctx context.Context, scope Scope, key string) error {
	if scope == "" || key == "" {
		return errors.New("state.KV.Delete: scope and key required")
	}
	_, err := kv.db.ExecContext(ctx, `DELETE FROM kv WHERE scope = $1 AND key = $2`, string(scope), key)
	return err
}

// ListByPrefix returns every (key, raw JSON) pair whose key starts with prefix
// inside the given scope. Cap is 1000 — callers needing more should paginate.
func (kv *KV) ListByPrefix(ctx context.Context, scope Scope, prefix string) (map[string]string, error) {
	rows, err := kv.db.QueryContext(ctx,
		`SELECT key, value_json FROM kv WHERE scope = $1 AND key LIKE $2 ORDER BY key LIMIT 1000`,
		string(scope), prefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
