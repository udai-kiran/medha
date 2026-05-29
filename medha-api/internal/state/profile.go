package state

import (
	"context"
	"encoding/json"
)

// ProjectProfile is the aggregated intelligence snapshot for a project.
type ProjectProfile struct {
	Project     string         `json:"project"`
	TopConcepts []ConceptCount `json:"topConcepts"`
	TopFiles    []FileCount    `json:"topFiles"`
	MemoryTypes []TypeCount    `json:"memoryTypes"`
	TotalObs    int            `json:"totalObservations"`
	TotalMem    int            `json:"totalMemories"`
}

// ConceptCount is a concept string with its frequency across observations.
type ConceptCount struct {
	Concept string `json:"concept"`
	Count   int    `json:"count"`
}

// FileCount is a file path with how many observations touched it.
type FileCount struct {
	FilePath string `json:"filePath"`
	Count    int    `json:"count"`
}

// TypeCount is a memory type with its count.
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// ProjectProfile aggregates top concepts (from compressed observations),
// top files, and memory type distribution for a project.
func (s *Store) ProjectProfile(ctx context.Context, project string) (*ProjectProfile, error) {
	p := &ProjectProfile{Project: project}

	// Total observations.
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM observations WHERE ($1 = '' OR project = $2) AND compressed = 1`,
		project, project,
	).Scan(&p.TotalObs)

	// Total memories.
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE ($1 = '' OR project = $2)`,
		project, project,
	).Scan(&p.TotalMem)

	// Top concepts: unnest concepts_json arrays and count occurrences.
	// We fetch all concepts_json values and aggregate in Go to avoid
	// requiring a specific PostgreSQL unnest+jsonb extension version.
	conceptCounts := map[string]int{}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT concepts_json FROM observations
         WHERE ($1 = '' OR project = $2) AND compressed = 1 AND concepts_json IS NOT NULL
         LIMIT 5000`,
		project, project,
	)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var raw string
			if rows.Scan(&raw) == nil {
				var concepts []string
				if json.Unmarshal([]byte(raw), &concepts) == nil {
					for _, c := range concepts {
						if c != "" {
							conceptCounts[c]++
						}
					}
				}
			}
		}
	}
	p.TopConcepts = topN(conceptCounts, 20)

	// Top files: same aggregation from files_json.
	fileCounts := map[string]int{}
	frows, err := s.DB.QueryContext(ctx,
		`SELECT files_json FROM observations
         WHERE ($1 = '' OR project = $2) AND compressed = 1 AND files_json IS NOT NULL
         LIMIT 5000`,
		project, project,
	)
	if err == nil {
		defer func() { _ = frows.Close() }()
		for frows.Next() {
			var raw string
			if frows.Scan(&raw) == nil {
				var files []string
				if json.Unmarshal([]byte(raw), &files) == nil {
					for _, f := range files {
						if f != "" {
							fileCounts[f]++
						}
					}
				}
			}
		}
	}
	p.TopFiles = topNFiles(fileCounts, 20)

	// Memory type distribution.
	trows, err := s.DB.QueryContext(ctx,
		`SELECT COALESCE(type,'unknown'), COUNT(*) FROM memories
         WHERE ($1 = '' OR project = $2)
         GROUP BY type ORDER BY COUNT(*) DESC`,
		project, project,
	)
	if err == nil {
		defer func() { _ = trows.Close() }()
		for trows.Next() {
			var tc TypeCount
			if trows.Scan(&tc.Type, &tc.Count) == nil {
				p.MemoryTypes = append(p.MemoryTypes, tc)
			}
		}
	}

	return p, nil
}

// topN returns the top n entries from a count map sorted by count desc.
func topN(m map[string]int, n int) []ConceptCount {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v})
	}
	// Simple insertion sort for small n.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].v > items[j-1].v; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	if len(items) > n {
		items = items[:n]
	}
	out := make([]ConceptCount, len(items))
	for i, it := range items {
		out[i] = ConceptCount{Concept: it.k, Count: it.v}
	}
	return out
}

func topNFiles(m map[string]int, n int) []FileCount {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v})
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].v > items[j-1].v; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	if len(items) > n {
		items = items[:n]
	}
	out := make([]FileCount, len(items))
	for i, it := range items {
		out[i] = FileCount{FilePath: it.k, Count: it.v}
	}
	return out
}
