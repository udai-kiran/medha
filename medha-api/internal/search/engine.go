package search

import "context"

// Hit is a single ranked result. Score range and meaning depend on the engine
// — RRF normalises across engines.
type Hit struct {
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
	// Provenance carries the source label (user|extracted|episodic|system) from
	// the FTS index so the hybrid orchestrator can apply priority boosts after
	// RRF fusion. Empty for vector/graph hits.
	Provenance string `json:"provenance,omitempty"`
}

// Engine is the contract every single-modality engine implements
// (FTS, vector, graph). The hybrid orchestrator consumes this.
type Engine interface {
	// Search returns up to ``limit`` ranked hits for the given project + query.
	Search(ctx context.Context, project, query string, limit int) ([]Hit, error)
}
