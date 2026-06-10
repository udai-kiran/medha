package search

import (
	"context"
	"math"
	"sort"
	"time"
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
	ModeBM25   = "bm25" // kept for API compat — routes to the PgFTS engine
	ModeVector = "vector"
	ModeGraph  = "graph"
	ModeHybrid = "hybrid"
)

// Hybrid orchestrates the three engines and fuses results via RRF, then
// optionally reranks the fused pool with a cross-encoder.
type Hybrid struct {
	// FTS is the PostgreSQL full-text search engine (replaces hand-rolled BM25).
	FTS    *PgFTS
	Vector *VectorIndex
	Graph  *GraphIndex
	// QueryExpander converts the natural-language query to structured ts_query
	// strings before the FTS leg of hybrid search. nil falls back to
	// websearch_to_tsquery. Failures are silently ignored.
	QueryExpander TSQueryExpander
	// Reranker reorders the RRF-fused pool using a cross-encoder. nil disables.
	Reranker Reranker
	// RerankPoolSize is the number of RRF hits fed to the reranker. 0 uses the
	// stage constant (30). Has no effect when Reranker is nil.
	RerankPoolSize int
	// PerGroupCap limits the number of results per diversity group (sessionId).
	// Applied after reranking. 0 disables.
	PerGroupCap int
	// K is the RRF k constant.
	K int
	// VectorTimeout bounds the vector leg inside hybrid search. 0 means no extra bound.
	VectorTimeout time.Duration
	// LookupGroup maps a Hit id to its diversity group. nil disables grouping.
	LookupGroup func(ctx context.Context, id string) string
	// RecencyWeight controls the post-RRF recency boost. The fused score of each
	// hit is multiplied by (1 + RecencyWeight·exp(-ageDays/RecencyHalfLifeDays)),
	// where age is derived from the doc's indexed_at timestamp. 0 disables the
	// boost entirely (pure relevance), preserving legacy ordering.
	RecencyWeight float64
	// RecencyHalfLifeDays is the age (in days) at which the recency bonus halves.
	// Ignored when RecencyWeight is 0; defaults to 7 days when unset but enabled.
	RecencyHalfLifeDays float64
}

// recencyBoost returns the score multiplier for a hit of the given age. A
// freshly-indexed hit (age 0) gets the full 1+weight bonus; the bonus decays
// exponentially with a configurable half-life so older sessions fade smoothly
// toward the 1.0 (no-boost) baseline. This mirrors the recency term in the
// Generative Agents retrieval score (Park et al., 2023).
func recencyBoost(ageDays, weight, halfLifeDays float64) float64 {
	if weight <= 0 {
		return 1.0
	}
	if halfLifeDays <= 0 {
		halfLifeDays = 7.0
	}
	if ageDays < 0 {
		ageDays = 0
	}
	// exp(-age·ln2/halfLife): bonus halves every halfLifeDays.
	decay := math.Exp(-ageDays * math.Ln2 / halfLifeDays)
	return 1.0 + weight*decay
}

// Search routes by mode. "hybrid" (and "bm25" for backward compat) run the full
// pipeline; the other modes call exactly one engine.
func (h *Hybrid) Search(ctx context.Context, project, query, mode string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	switch mode {
	case ModeBM25: // "bm25" now routes to the PgFTS engine
		if h.FTS == nil {
			return nil, nil
		}
		return h.FTS.Search(ctx, project, query, limit)
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

// provenanceBoost returns the score multiplier for a memory provenance label.
// Applied after RRF fusion so user-authored memories surface above pipeline
// extractions, which surface above raw episodic observations.
func provenanceBoost(p string) float64 {
	switch p {
	case "user":
		return 2.0
	case "extracted":
		return 1.0
	case "episodic":
		return 0.7
	default:
		return 1.0
	}
}

func (h *Hybrid) hybridSearch(ctx context.Context, project, query string, limit int) ([]Hit, error) {
	// Each engine returns up to stage hits so the RRF pool has room to rerank.
	const stage = 30
	var lists [][]Hit

	// Track provenance from FTS hits so we can boost after RRF (rank-based fusion
	// doesn't carry scores forward, so we apply the boost on the fused scores).
	provenanceByID := make(map[string]string)

	if h.FTS != nil {
		var ftsHits []Hit
		// Try LLM-expanded ts_query strings first; degrade to websearch_to_tsquery.
		if h.QueryExpander != nil {
			if tsqs, err := h.QueryExpander.Expand(ctx, query); err == nil && len(tsqs) > 0 {
				ftsHits, _ = h.FTS.SearchWithTSQuery(ctx, project, tsqs, stage)
			}
		}
		if len(ftsHits) == 0 {
			ftsHits, _ = h.FTS.Search(ctx, project, query, stage)
		}
		for _, fh := range ftsHits {
			if fh.Provenance != "" {
				provenanceByID[fh.ID] = fh.Provenance
			}
		}
		if len(ftsHits) > 0 {
			lists = append(lists, ftsHits)
		}
	}
	if h.Vector != nil {
		vectorCtx := ctx
		var cancel context.CancelFunc
		if h.VectorTimeout > 0 {
			vectorCtx, cancel = context.WithTimeout(ctx, h.VectorTimeout)
		}
		if hs, err := h.Vector.Search(vectorCtx, project, query, stage); err == nil && hs != nil {
			lists = append(lists, hs)
		}
		if cancel != nil {
			cancel()
		}
	}
	if h.Graph != nil {
		if hs, err := h.Graph.Search(ctx, project, query, stage); err == nil && hs != nil {
			lists = append(lists, hs)
		}
	}
	if len(lists) == 0 {
		return nil, nil
	}
	fused := RRFFuse(h.K, lists...)

	// Apply provenance boost: user memories rise above pipeline extractions which
	// rise above episodic observations. Hits may enter through vector or graph
	// without an FTS hit, so batch-load missing provenance from pgfts_docs before
	// applying multipliers.
	if h.FTS != nil && len(fused) > 0 {
		missing := make([]string, 0, len(fused))
		for _, hit := range fused {
			if _, ok := provenanceByID[hit.ID]; !ok {
				missing = append(missing, hit.ID)
			}
		}
		if len(missing) > 0 {
			if provenances, err := h.FTS.ProvenanceFor(ctx, missing); err == nil {
				for id, provenance := range provenances {
					provenanceByID[id] = provenance
				}
			}
		}
	}
	if len(provenanceByID) > 0 {
		for i := range fused {
			if p, ok := provenanceByID[fused[i].ID]; ok {
				fused[i].Score *= provenanceBoost(p)
			}
		}
		sort.SliceStable(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
	}

	// Apply recency boost: recently-indexed memories/observations rise above
	// older ones so the current and recent sessions outrank stale ones. The
	// indexed_at timestamp lives in the FTS table and covers every leg (FTS,
	// vector, graph), so one batch lookup serves the whole fused pool. Best-effort:
	// a lookup failure leaves the relevance-only order untouched.
	if h.RecencyWeight > 0 && h.FTS != nil && len(fused) > 0 {
		ids := make([]string, len(fused))
		for i := range fused {
			ids[i] = fused[i].ID
		}
		if indexedAt, err := h.FTS.IndexedAt(ctx, ids); err == nil && len(indexedAt) > 0 {
			now := time.Now()
			for i := range fused {
				ts, ok := indexedAt[fused[i].ID]
				if !ok {
					continue
				}
				ageDays := now.Sub(ts).Hours() / 24.0
				fused[i].Score *= recencyBoost(ageDays, h.RecencyWeight, h.RecencyHalfLifeDays)
			}
			sort.SliceStable(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
		}
	}

	// Cross-encoder reranking. Best-effort: any failure falls back to RRF order.
	if h.Reranker != nil && h.FTS != nil && len(fused) > 0 {
		pool := fused
		poolSize := h.RerankPoolSize
		if poolSize <= 0 {
			poolSize = stage
		}
		if len(pool) > poolSize {
			pool = pool[:poolSize]
		}
		ids := make([]string, len(pool))
		for i, hit := range pool {
			ids[i] = hit.ID
		}
		if texts, err := h.FTS.GetDocumentTexts(ctx, ids); err == nil {
			candidates := make([]RerankCandidate, len(pool))
			for i, hit := range pool {
				candidates[i] = RerankCandidate{ID: hit.ID, Text: texts[hit.ID]}
			}
			if reranked, err := h.Reranker.Rerank(ctx, query, candidates); err == nil && len(reranked) > 0 {
				fused = reranked
			}
		}
	}

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
