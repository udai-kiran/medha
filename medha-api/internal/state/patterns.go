package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// PatternRow is a detected recurring pattern row.
type PatternRow struct {
	ID        string
	Project   string
	Pattern   string
	Count     int
	Examples  []PatternExample
	LastSeen  string
	CreatedAt string
}

// PatternExample is a single example observation that matched the pattern.
type PatternExample struct {
	ObsID string `json:"obsId"`
	Title string `json:"title"`
}

// DetectAndSavePatterns scans recent compressed observations for the project,
// identifies co-occurrence patterns in concept pairs and file→concept pairs,
// and upserts them into the patterns table.
// It returns the top-n patterns by count.
func (s *Store) DetectAndSavePatterns(ctx context.Context, project string, limit int) ([]*PatternRow, error) {
	if limit <= 0 {
		limit = 20
	}

	// Fetch recent compressed observations for concept/file mining.
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, COALESCE(title,''), COALESCE(concepts_json,'[]'), COALESCE(files_json,'[]')
        FROM observations
        WHERE ($1 = '' OR project = $2) AND compressed = 1
        ORDER BY created_at DESC LIMIT 2000
    `, project, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	type obsData struct {
		id       string
		title    string
		concepts []string
		files    []string
	}
	var obs []obsData
	for rows.Next() {
		var o obsData
		var conceptsRaw, filesRaw string
		if err := rows.Scan(&o.id, &o.title, &conceptsRaw, &filesRaw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(conceptsRaw), &o.concepts)
		_ = json.Unmarshal([]byte(filesRaw), &o.files)
		obs = append(obs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Count concept-pair co-occurrences as patterns.
	type patternKey = string
	counts := map[patternKey]int{}
	examples := map[patternKey][]PatternExample{}

	for _, o := range obs {
		// Concept pairs.
		for i := 0; i < len(o.concepts); i++ {
			for j := i + 1; j < len(o.concepts); j++ {
				pair := sortedPair(o.concepts[i], o.concepts[j])
				counts[pair]++
				if len(examples[pair]) < 3 {
					examples[pair] = append(examples[pair], PatternExample{ObsID: o.id, Title: o.title})
				}
			}
		}
		// File → concept associations.
		for _, f := range o.files {
			base := baseName(f)
			for _, c := range o.concepts {
				key := base + " → " + c
				counts[key]++
				if len(examples[key]) < 3 {
					examples[key] = append(examples[key], PatternExample{ObsID: o.id, Title: o.title})
				}
			}
		}
	}

	// Filter to patterns seen at least twice.
	type ranked struct {
		pattern string
		count   int
	}
	var rankings []ranked
	for k, v := range counts {
		if v >= 2 {
			rankings = append(rankings, ranked{k, v})
		}
	}
	sort.Slice(rankings, func(i, j int) bool { return rankings[i].count > rankings[j].count })
	if len(rankings) > limit {
		rankings = rankings[:limit]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var result []*PatternRow
	for _, r := range rankings {
		exJSON, _ := json.Marshal(examples[r.pattern])
		id := newPatternID(project, r.pattern)
		_, err := s.DB.ExecContext(ctx, `
            INSERT INTO patterns (id, project, pattern, count, examples, last_seen, created_at)
            VALUES ($1, $2, $3, $4, $5, $6, $6)
            ON CONFLICT(id) DO UPDATE SET
                count     = GREATEST(patterns.count, excluded.count),
                examples  = excluded.examples,
                last_seen = excluded.last_seen
        `, id, project, r.pattern, r.count, string(exJSON), now)
		if err != nil {
			continue
		}
		var ex []PatternExample
		_ = json.Unmarshal(exJSON, &ex)
		result = append(result, &PatternRow{
			ID: id, Project: project, Pattern: r.pattern,
			Count: r.count, Examples: ex, LastSeen: now, CreatedAt: now,
		})
	}
	return result, nil
}

// ListPatterns returns saved patterns for a project ordered by count desc.
func (s *Store) ListPatterns(ctx context.Context, project string, limit int) ([]*PatternRow, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, project, pattern, count, examples, last_seen, created_at
        FROM patterns
        WHERE ($1 = '' OR project = $2)
        ORDER BY count DESC LIMIT $3
    `, project, project, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*PatternRow
	for rows.Next() {
		p := &PatternRow{}
		var exRaw string
		if err := rows.Scan(&p.ID, &p.Project, &p.Pattern, &p.Count, &exRaw, &p.LastSeen, &p.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(exRaw), &p.Examples)
		out = append(out, p)
	}
	return out, rows.Err()
}

func sortedPair(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + " + " + b
}

func baseName(path string) string {
	i := strings.LastIndexAny(path, "/\\")
	if i >= 0 {
		return path[i+1:]
	}
	return path
}

func newPatternID(project, pattern string) string {
	// Deterministic ID so upsert by ID works.
	h := fnv32(project + ":" + pattern)
	return "pat-" + uint32Hex(h)
}

func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func uint32Hex(v uint32) string {
	const hexChars = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = hexChars[v&0xf]
		v >>= 4
	}
	return string(b)
}
