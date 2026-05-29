package search

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/udai-kiran/medha/internal/state"
)

// Entity is the in-memory view of a graph_entities row, narrowed to the
// fields the search path needs.
type Entity struct {
	ID         string
	Name       string
	Type       string
	Subtype    string
	Confidence float64
}

// Edge is a directed, typed relationship between two entities.
type Edge struct {
	ID                  string
	SourceID            string
	TargetID            string
	Type                string
	Confidence          float64
	SourceObservationID string
}

// GraphIndex is the PostgreSQL-backed knowledge graph. It implements SearchEngine
// — query → entity match → BFS-2 → observations that referenced any reachable
// entity. Neo4j enrichment layers on top via a separate store.
type GraphIndex struct {
	store *state.Store
	// BFS depth is configurable; default = 2.
	MaxDepth int
	// MinConfidence filters edges below this threshold during traversal.
	MinConfidence float64
}

// NewGraphIndex returns a GraphIndex over an open Store.
func NewGraphIndex(s *state.Store) *GraphIndex {
	return &GraphIndex{store: s, MaxDepth: 2, MinConfidence: 0.3}
}

// pgPlaceholders returns "$start, $start+1, ..." for n items.
func pgPlaceholders(start, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", start+i)
	}
	return strings.Join(parts, ", ")
}

// UpsertEntity inserts or updates an entity by (project, name, type) tuple.
// Returns the stored entity (with id populated).
func (g *GraphIndex) UpsertEntity(ctx context.Context, project, name, typ, subtype string, confidence float64) (*Entity, error) {
	if name == "" || typ == "" {
		return nil, errors.New("UpsertEntity: name and type required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := newEntityID()

	res, err := g.store.DB.ExecContext(ctx, `
        INSERT INTO graph_entities (id, project, name, type, subtype, confidence, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT(project, name, type) DO UPDATE SET
            confidence = GREATEST(graph_entities.confidence, excluded.confidence),
            subtype    = COALESCE(NULLIF(excluded.subtype, ''), graph_entities.subtype),
            updated_at = excluded.updated_at
    `, id, project, name, typ, subtype, confidence, now, now)
	if err != nil {
		return nil, err
	}
	_ = res

	row := g.store.DB.QueryRowContext(ctx,
		`SELECT id, name, type, subtype, confidence FROM graph_entities WHERE project = $1 AND name = $2 AND type = $3`,
		project, name, typ,
	)
	out := &Entity{}
	var subtypeStr sql.NullString
	if err := row.Scan(&out.ID, &out.Name, &out.Type, &subtypeStr, &out.Confidence); err != nil {
		return nil, err
	}
	out.Subtype = subtypeStr.String
	return out, nil
}

// AddEdge persists a typed relationship between two entities.
func (g *GraphIndex) AddEdge(ctx context.Context, project string, e Edge) error {
	if e.SourceID == "" || e.TargetID == "" || e.Type == "" {
		return errors.New("AddEdge: source_id, target_id, type required")
	}
	if e.ID == "" {
		e.ID = newEdgeID()
	}
	_, err := g.store.DB.ExecContext(ctx, `
        INSERT INTO graph_edges (id, project, source_id, target_id, type, confidence, source_observation_id, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
    `, e.ID, project, e.SourceID, e.TargetID, e.Type, e.Confidence, e.SourceObservationID,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// LinkObservationToEntity records that an observation referenced an entity.
func (g *GraphIndex) LinkObservationToEntity(ctx context.Context, observationID, entityID string) error {
	_, err := g.store.DB.ExecContext(ctx, `
        INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
        VALUES ($1, 'system', 'link', 'entity', $2, $3)
    `, time.Now().UTC().Format(time.RFC3339Nano), entityID,
		fmt.Sprintf(`{"observation_id":%q}`, observationID))
	return err
}

// MatchEntities returns entities whose name fuzzily matches a query term.
func (g *GraphIndex) MatchEntities(ctx context.Context, project, query string) ([]Entity, error) {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	// $1, $2 = project (for the project filter); $3+ = LIKE patterns
	args := []any{project, project}
	conds := make([]string, 0, len(tokens))
	for i, t := range tokens {
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE $%d", i+3))
		args = append(args, "%"+strings.ToLower(t)+"%")
	}
	q := `SELECT id, name, type, COALESCE(subtype, ''), confidence
          FROM graph_entities
          WHERE ($1 = '' OR project = $2)
          AND (` + strings.Join(conds, " OR ") + `)
          ORDER BY confidence DESC LIMIT 32`

	rows, err := g.store.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Entity
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.Name, &e.Type, &e.Subtype, &e.Confidence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BFSTraverse returns the set of entity ids reachable from ``seeds`` within
// MaxDepth hops, following both incoming and outgoing edges.
func (g *GraphIndex) BFSTraverse(ctx context.Context, project string, seeds []string) (map[string]int, error) {
	visited := make(map[string]int, len(seeds))
	for _, id := range seeds {
		visited[id] = 0
	}
	frontier := append([]string(nil), seeds...)

	for depth := 1; depth <= g.MaxDepth && len(frontier) > 0; depth++ {
		n := len(frontier)
		// $1, $2 = project; $3..$n+2 = source IDs; $n+3..$2n+2 = target IDs; $2n+3 = minConfidence
		srcPlaceholders := pgPlaceholders(3, n)
		tgtPlaceholders := pgPlaceholders(3+n, n)
		confidenceParam := fmt.Sprintf("$%d", 3+2*n)

		q := fmt.Sprintf(`
            SELECT source_id, target_id FROM graph_edges
            WHERE ($1 = '' OR project = $2)
            AND (source_id IN (%s) OR target_id IN (%s))
            AND confidence >= %s
        `, srcPlaceholders, tgtPlaceholders, confidenceParam)

		args := make([]any, 0, 2*n+3)
		args = append(args, project, project)
		for _, id := range frontier {
			args = append(args, id)
		}
		for _, id := range frontier {
			args = append(args, id)
		}
		args = append(args, g.MinConfidence)

		rows, err := g.store.DB.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		next := []string{}
		for rows.Next() {
			var src, tgt string
			if err := rows.Scan(&src, &tgt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			for _, nd := range []string{src, tgt} {
				if _, ok := visited[nd]; ok {
					continue
				}
				visited[nd] = depth
				next = append(next, nd)
			}
		}
		_ = rows.Close()
		frontier = next
	}
	return visited, nil
}

// Search satisfies SearchEngine: query → matched entities → BFS → observations.
func (g *GraphIndex) Search(ctx context.Context, project, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	matched, err := g.MatchEntities(ctx, project, query)
	if err != nil {
		return nil, err
	}
	if len(matched) == 0 {
		return nil, nil
	}
	seeds := make([]string, 0, len(matched))
	seedConf := make(map[string]float64, len(matched))
	for _, e := range matched {
		seeds = append(seeds, e.ID)
		seedConf[e.ID] = e.Confidence
	}

	visited, err := g.BFSTraverse(ctx, project, seeds)
	if err != nil {
		return nil, err
	}
	if len(visited) == 0 {
		return nil, nil
	}

	entIDs := make([]string, 0, len(visited))
	for id := range visited {
		entIDs = append(entIDs, id)
	}
	placeholders := pgPlaceholders(1, len(entIDs))
	args := make([]any, 0, len(entIDs))
	for _, id := range entIDs {
		args = append(args, id)
	}
	rows, err := g.store.DB.QueryContext(ctx, `
        SELECT target_id, payload_json FROM audit_log
        WHERE action = 'link' AND target_type = 'entity'
        AND target_id IN (`+placeholders+`)
    `, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	scores := make(map[string]float64)
	for rows.Next() {
		var entityID, payload string
		if err := rows.Scan(&entityID, &payload); err != nil {
			return nil, err
		}
		obsID := extractObservationID(payload)
		if obsID == "" {
			continue
		}
		depth := visited[entityID]
		conf := seedConf[entityID]
		if conf == 0 {
			conf = 0.5
		}
		scores[obsID] += conf / float64(1+depth)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hits := make([]Hit, 0, len(scores))
	for id, s := range scores {
		hits = append(hits, Hit{ID: id, Score: s})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// extractObservationID pulls `"observation_id":"obs-..."` out of a JSON blob
// without a full parse.
func extractObservationID(payload string) string {
	const key = `"observation_id":"`
	i := strings.Index(payload, key)
	if i < 0 {
		return ""
	}
	rest := payload[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func newEntityID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "ent-" + hex.EncodeToString(b[:])
}

func newEdgeID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "edg-" + hex.EncodeToString(b[:])
}

// Compile-time assertion that GraphIndex satisfies SearchEngine.
var _ SearchEngine = (*GraphIndex)(nil)
