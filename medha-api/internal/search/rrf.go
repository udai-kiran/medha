package search

import (
	"context"
	"sort"
)

// RRFFuse combines multiple ranked lists with Reciprocal Rank Fusion.
//
//	score(d) = Σ_lists 1 / (k + rank_list(d))
//
// k=60 is the agent_mem.md default. Returned scores carry only relative
// meaning — the absolute value depends on how many lists fed in.
func RRFFuse(k int, lists ...[]Hit) []Hit {
	if k <= 0 {
		k = 60
	}
	scores := make(map[string]float64)
	for _, list := range lists {
		for rank, h := range list {
			// rank is 0-indexed; +1 so the top hit is rank 1.
			scores[h.ID] += 1.0 / float64(k+rank+1)
		}
	}
	out := make([]Hit, 0, len(scores))
	for id, s := range scores {
		out = append(out, Hit{ID: id, Score: s})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// DiversityBoost caps the number of hits per "group" (e.g. sessionId) so a
// single noisy session doesn't dominate. Group is supplied via the lookup
// callback so this stays orthogonal to the Hit shape.
func DiversityBoost(hits []Hit, perGroup int, group func(id string) string) []Hit {
	if perGroup <= 0 || group == nil {
		return hits
	}
	count := make(map[string]int)
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		g := group(h.ID)
		if g == "" || count[g] < perGroup {
			out = append(out, h)
			count[g]++
		}
	}
	return out
}

// Mode names accepted by the orchestrator.
const (
	ModeBM25   = "bm25"
	ModeVector = "vector"
	ModeGraph  = "graph"
	ModeHybrid = "hybrid"
)

// Hybrid orchestrates the three engines and fuses results via RRF. Each
// engine is independent; missing engines (e.g. graph in lightweight mode)
// are silently skipped.
type Hybrid struct {
	BM25   *BM25
	Vector *VectorIndex
	Graph  *GraphIndex
	// PerGroupCap limits the number of results sharing the same diversity
	// group (typically sessionId). 0 disables.
	PerGroupCap int
	// K is the RRF k constant.
	K int
	// LookupGroup maps a Hit id to its diversity group. nil disables grouping.
	LookupGroup func(ctx context.Context, id string) string
}

// Search routes by mode. "hybrid" runs all engines and fuses; the other modes
// call exactly one engine and return its native ranking.
func (h *Hybrid) Search(ctx context.Context, project, query, mode string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	switch mode {
	case ModeBM25:
		if h.BM25 == nil {
			return nil, nil
		}
		return h.BM25.Search(ctx, project, query, limit)
	case ModeVector:
		if h.Vector == nil {
			return nil, nil
		}
		return h.Vector.Search(ctx, project, query, limit)
	case ModeGraph:
		if h.Graph == nil {
			return nil, nil
		}
		return h.Graph.Search(ctx, project, query, limit)
	default:
		return h.hybridSearch(ctx, project, query, limit)
	}
}

func (h *Hybrid) hybridSearch(ctx context.Context, project, query string, limit int) ([]Hit, error) {
	// Each engine returns up to 30 (per agent_mem.md "Phase 2") so the fusion
	// has enough room to re-rank.
	const stage = 30
	var lists [][]Hit
	if h.BM25 != nil {
		hs, err := h.BM25.Search(ctx, project, query, stage)
		if err == nil && hs != nil {
			lists = append(lists, hs)
		}
	}
	if h.Vector != nil {
		hs, err := h.Vector.Search(ctx, project, query, stage)
		if err == nil && hs != nil {
			lists = append(lists, hs)
		}
	}
	if h.Graph != nil {
		hs, err := h.Graph.Search(ctx, project, query, stage)
		if err == nil && hs != nil {
			lists = append(lists, hs)
		}
	}
	if len(lists) == 0 {
		return nil, nil
	}
	fused := RRFFuse(h.K, lists...)

	if h.PerGroupCap > 0 && h.LookupGroup != nil {
		fused = DiversityBoost(fused, h.PerGroupCap, func(id string) string {
			return h.LookupGroup(ctx, id)
		})
	}
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused, nil
}
