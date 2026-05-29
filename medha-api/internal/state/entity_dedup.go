package state

import (
	"context"
	"strings"
	"time"
)

// SameAsRow represents a potential duplicate relationship between two entities.
type SameAsRow struct {
	SourceID   string
	TargetID   string
	Confidence float64
	MatchType  string // exact | fuzzy | semantic
	Status     string // pending | confirmed | rejected
	CreatedAt  string
}

// UpsertSameAs records a potential duplicate pair.
func (s *Store) UpsertSameAs(ctx context.Context, sourceID, targetID string, confidence float64, matchType string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx, `
        INSERT INTO entity_same_as (source_id, target_id, confidence, match_type, status, created_at)
        VALUES ($1, $2, $3, $4, 'pending', $5)
        ON CONFLICT(source_id, target_id) DO UPDATE SET
            confidence = GREATEST(entity_same_as.confidence, excluded.confidence),
            match_type = excluded.match_type
    `, sourceID, targetID, confidence, matchType, now)
	return err
}

// ReviewDuplicate confirms or rejects a SAME_AS pair.
func (s *Store) ReviewDuplicate(ctx context.Context, sourceID, targetID string, confirm bool) error {
	status := "rejected"
	if confirm {
		status = "confirmed"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE entity_same_as SET status = $1, reviewed_at = $2
         WHERE source_id = $3 AND target_id = $4`,
		status, now, sourceID, targetID)
	return err
}

// FindPotentialDuplicates returns pending SAME_AS pairs above a confidence threshold.
func (s *Store) FindPotentialDuplicates(ctx context.Context, project string, minConfidence float64, limit int) ([]*SameAsRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
        SELECT sa.source_id, sa.target_id, sa.confidence, sa.match_type, sa.status, sa.created_at
        FROM entity_same_as sa
        JOIN graph_entities ge ON ge.id = sa.source_id
        WHERE sa.status = 'pending' AND sa.confidence >= $1
        AND ($2 = '' OR ge.project = $3)
        ORDER BY sa.confidence DESC LIMIT $4
    `, minConfidence, project, project, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*SameAsRow
	for rows.Next() {
		r := &SameAsRow{}
		if err := rows.Scan(&r.SourceID, &r.TargetID, &r.Confidence, &r.MatchType, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CheckAndFlagDuplicates runs name-based dedup against existing entities for
// a newly upserted entity. Flags near-matches for review.
// Returns number of potential duplicates found.
func (s *Store) CheckAndFlagDuplicates(ctx context.Context, entityID, project, name, entityType string) (int, error) {
	// Exact name + type match in same project → high confidence.
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id FROM graph_entities
        WHERE project = $1 AND LOWER(name) = LOWER($2) AND type = $3 AND id != $4
        LIMIT 10
    `, project, name, entityType, entityID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var targetID string
		if rows.Scan(&targetID) == nil {
			_ = s.UpsertSameAs(ctx, entityID, targetID, 0.98, "exact")
			count++
		}
	}

	// Fuzzy: similar name (Levenshtein distance ≤ 2) via LIKE heuristic.
	// Use first 3 chars as a fast filter.
	if len(name) >= 3 {
		prefix := strings.ToLower(name[:3])
		frows, err := s.DB.QueryContext(ctx, `
            SELECT id, name FROM graph_entities
            WHERE project = $1 AND type = $2 AND id != $3
            AND LOWER(name) LIKE $4
            LIMIT 20
        `, project, entityType, entityID, prefix+"%")
		if err == nil {
			defer func() { _ = frows.Close() }()
			for frows.Next() {
				var tid, tname string
				if frows.Scan(&tid, &tname) == nil {
					dist := levenshtein(strings.ToLower(name), strings.ToLower(tname))
					if dist <= 2 && dist > 0 {
						conf := 1.0 - float64(dist)*0.1
						_ = s.UpsertSameAs(ctx, entityID, tid, conf, "fuzzy")
						count++
					}
				}
			}
		}
	}
	return count, nil
}

// levenshtein computes edit distance between two strings (capped at 3 for perf).
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) > 20 || len(b) > 20 {
		// Skip expensive computation for long strings.
		return 3
	}
	la, lb := len(a), len(b)
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min3(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
		}
	}
	return d[la][lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
