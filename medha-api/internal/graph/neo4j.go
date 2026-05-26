// Package graph holds the Neo4j graph store. The SQLite-backed graph in
// internal/search (Task 16) is the always-on path; Neo4j (ADR-0003) is an
// optional enrichment layer for projects with the appetite to run it.
package graph

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Store is the agent_mem view of Neo4j. It is intentionally thin — the
// Cypher queries we need are small and the heavy lifting (rank fusion,
// hybrid search) stays in Go.
type Store struct {
	driver neo4j.DriverWithContext
	Logger *slog.Logger
}

// Config is the connection config for the Neo4j store.
type Config struct {
	URI      string
	Username string
	Password string
	Logger   *slog.Logger
}

// Open dials Neo4j and verifies connectivity. Caller must Close.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.URI == "" {
		return nil, errors.New("graph.Open: URI required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	drv, err := neo4j.NewDriverWithContext(
		cfg.URI,
		neo4j.BasicAuth(cfg.Username, cfg.Password, ""),
	)
	if err != nil {
		return nil, fmt.Errorf("graph.Open: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := drv.VerifyConnectivity(connectCtx); err != nil {
		_ = drv.Close(ctx)
		return nil, fmt.Errorf("graph.Open: connectivity: %w", err)
	}
	s := &Store{driver: drv, Logger: cfg.Logger}
	if err := s.ensureIndexes(ctx); err != nil {
		s.Logger.Warn("graph.ensure_indexes_failed", "err", err)
	}
	return s, nil
}

// Close releases the driver.
func (s *Store) Close(ctx context.Context) error {
	if s == nil || s.driver == nil {
		return nil
	}
	return s.driver.Close(ctx)
}

// ensureIndexes creates the per-project entity name index. Idempotent.
func (s *Store) ensureIndexes(ctx context.Context) error {
	queries := []string{
		`CREATE CONSTRAINT entity_id IF NOT EXISTS
            FOR (e:Entity) REQUIRE e.id IS UNIQUE`,
		`CREATE INDEX entity_name IF NOT EXISTS
            FOR (e:Entity) ON (e.name)`,
		`CREATE INDEX entity_project IF NOT EXISTS
            FOR (e:Entity) ON (e.project)`,
	}
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{})
	defer func() { _ = session.Close(ctx) }()
	for _, q := range queries {
		if _, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			_, err := tx.Run(ctx, q, nil)
			return nil, err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Entity is the minimal entity shape this store handles. Wider properties
// (enriched description, image, wikidata id) live in the same node as
// optional properties — they're written/read by the enrichment path (Task 30).
type Entity struct {
	ID         string         `json:"id"`
	Project    string         `json:"project,omitempty"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Subtype    string         `json:"subtype,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// UpsertEntity merges (project, name, type) — leaves higher confidence wins.
func (s *Store) UpsertEntity(ctx context.Context, e Entity) error {
	if e.Name == "" || e.Type == "" {
		return errors.New("UpsertEntity: name and type required")
	}
	params := map[string]any{
		"id":         e.ID,
		"project":    e.Project,
		"name":       e.Name,
		"type":       e.Type,
		"subtype":    e.Subtype,
		"confidence": e.Confidence,
	}
	if e.Extra != nil {
		params["extra"] = e.Extra
	}
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{})
	defer func() { _ = session.Close(ctx) }()

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
            MERGE (e:Entity {project: $project, name: $name, type: $type})
            ON CREATE SET e.id = coalesce($id, randomUuid()),
                          e.subtype = $subtype,
                          e.confidence = coalesce($confidence, 0.5),
                          e.created_at = datetime()
            ON MATCH  SET e.confidence = CASE WHEN $confidence > e.confidence THEN $confidence ELSE e.confidence END,
                          e.subtype = coalesce($subtype, e.subtype),
                          e.updated_at = datetime()
        `, params)
		return nil, err
	})
	return err
}

// Edge is a typed, directional relationship between two entities.
type Edge struct {
	SourceID            string  `json:"sourceId"`
	TargetID            string  `json:"targetId"`
	Type                string  `json:"type"`
	Confidence          float64 `json:"confidence,omitempty"`
	SourceObservationID string  `json:"sourceObservationId,omitempty"`
}

// AddEdge persists a relationship. Type becomes the Neo4j edge label.
func (s *Store) AddEdge(ctx context.Context, e Edge) error {
	if e.SourceID == "" || e.TargetID == "" || e.Type == "" {
		return errors.New("AddEdge: source, target, type required")
	}
	q := fmt.Sprintf(`
        MATCH (a:Entity {id: $src}), (b:Entity {id: $tgt})
        MERGE (a)-[r:%s]->(b)
        ON CREATE SET r.confidence = $conf, r.source_observation_id = $obs,
                      r.created_at = datetime()
        ON MATCH  SET r.confidence = CASE WHEN $conf > r.confidence THEN $conf ELSE r.confidence END
    `, sanitiseLabel(e.Type))
	params := map[string]any{
		"src": e.SourceID, "tgt": e.TargetID,
		"conf": e.Confidence, "obs": e.SourceObservationID,
	}
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{})
	defer func() { _ = session.Close(ctx) }()
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, q, params)
		return nil, err
	})
	return err
}

// Neighbours returns entities reachable within ``depth`` hops from any entity
// matching ``name`` (LIKE search, case-insensitive).
func (s *Store) Neighbours(ctx context.Context, project, name string, depth, limit int) ([]Entity, error) {
	if depth <= 0 {
		depth = 2
	}
	if limit <= 0 {
		limit = 25
	}
	q := fmt.Sprintf(`
        MATCH (e:Entity) WHERE ($project = '' OR e.project = $project)
        AND toLower(e.name) CONTAINS toLower($name)
        MATCH (e)-[*1..%d]-(n:Entity)
        WHERE n.id <> e.id
        RETURN DISTINCT n LIMIT $limit
    `, depth)
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{})
	defer func() { _ = session.Close(ctx) }()
	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		rows, err := tx.Run(ctx, q, map[string]any{"project": project, "name": name, "limit": limit})
		if err != nil {
			return nil, err
		}
		var out []Entity
		for rows.Next(ctx) {
			node, ok := rows.Record().Values[0].(neo4j.Node)
			if !ok {
				continue
			}
			e := Entity{
				ID:      stringProp(node.Props, "id"),
				Project: stringProp(node.Props, "project"),
				Name:    stringProp(node.Props, "name"),
				Type:    stringProp(node.Props, "type"),
				Subtype: stringProp(node.Props, "subtype"),
			}
			if v, ok := node.Props["confidence"].(float64); ok {
				e.Confidence = v
			}
			out = append(out, e)
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return res.([]Entity), nil
}

// Health pings the driver. Used by the /health endpoint to report optional
// component status.
func (s *Store) Health(ctx context.Context) error {
	return s.driver.VerifyConnectivity(ctx)
}

func stringProp(p map[string]any, k string) string {
	if v, ok := p[k].(string); ok {
		return v
	}
	return ""
}

// sanitiseLabel makes a string safe to splice into a Cypher edge label.
// Allowlist letters, digits, underscores. Reject empty.
func sanitiseLabel(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b >= 'A' && b <= 'Z',
			b >= 'a' && b <= 'z',
			b >= '0' && b <= '9',
			b == '_':
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return "RELATED_TO"
	}
	return string(out)
}
