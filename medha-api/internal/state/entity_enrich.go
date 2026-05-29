package state

import (
	"context"
	"encoding/json"
	"time"
)

// EnrichmentData holds data fetched from external sources (Wikipedia/Diffbot).
type EnrichmentData struct {
	Description  string `json:"description,omitempty"`
	WikipediaURL string `json:"wikipedia_url,omitempty"`
	WikidataID   string `json:"wikidata_id,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`
}

// SetEnrichment stores enrichment data on an entity.
func (s *Store) SetEnrichment(ctx context.Context, entityID string, data *EnrichmentData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.DB.ExecContext(ctx,
		`UPDATE graph_entities SET enriched_json = $1, updated_at = $2 WHERE id = $3`,
		string(raw), now, entityID)
	return err
}

// GetEnrichment reads enrichment data from an entity's enriched_json column.
func (s *Store) GetEnrichment(ctx context.Context, entityID string) (*EnrichmentData, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(enriched_json,'{}') FROM graph_entities WHERE id = $1`, entityID,
	).Scan(&raw)
	if err != nil {
		return nil, ErrNotFound
	}
	var data EnrichmentData
	_ = json.Unmarshal([]byte(raw), &data)
	return &data, nil
}

// SetGeocode stores lat/lon on a LOCATION entity (G14).
func (s *Store) SetGeocode(ctx context.Context, entityID string, latitude, longitude float64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE graph_entities SET latitude = $1, longitude = $2, geocoded_at = $3 WHERE id = $4`,
		latitude, longitude, now, entityID)
	return err
}

// SearchLocationsNear returns LOCATION entities within radius_km of the given point.
// Uses a simple bounding-box approximation (1 degree ≈ 111 km).
func (s *Store) SearchLocationsNear(ctx context.Context, project string, lat, lon, radiusKm float64, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	delta := radiusKm / 111.0
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, name, type, subtype, confidence, latitude, longitude
        FROM graph_entities
        WHERE type = 'LOCATION'
        AND latitude IS NOT NULL AND longitude IS NOT NULL
        AND ($1 = '' OR project = $2)
        AND latitude  BETWEEN $3 AND $4
        AND longitude BETWEEN $5 AND $6
        ORDER BY confidence DESC LIMIT $7
    `, project, project, lat-delta, lat+delta, lon-delta, lon+delta, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]any
	for rows.Next() {
		var id, name, typ, subtype string
		var conf, eLat, eLon float64
		if rows.Scan(&id, &name, &typ, &subtype, &conf, &eLat, &eLon) == nil {
			out = append(out, map[string]any{
				"id": id, "name": name, "type": typ, "subtype": subtype,
				"confidence": conf, "latitude": eLat, "longitude": eLon,
			})
		}
	}
	return out, rows.Err()
}
