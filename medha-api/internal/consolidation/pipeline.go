package consolidation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/udai-kiran/medha/internal/graph"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
)

// Pipeline runs the SessionEnd consolidation DAG:
//
//	1. Fetch CompressedObservation rows for the session.
//	2. POST /summarize on Python → SessionSummary.
//	3. POST /extract on each observation → entities + relationships → persist to graph.
//	4. Persist SessionSummary, link observations to entities, build edges,
//	   distil memories.
//
// The pipeline is best-effort: each step is independent and may fail
// without aborting the others. Python unreachable → fall back to a synthetic
// session summary built in Go from the raw observation rows.
// MemoryIndexer is the minimal interface for indexing memory content into
// BM25 + vector search after the consolidation pipeline persists a memory row.
type MemoryIndexer interface {
	IndexObservation(ctx context.Context, id, project, text string) error
}

type Pipeline struct {
	Store            *state.Store
	PythonServiceURL string
	HTTPClient       *http.Client
	Logger           *slog.Logger
	StepTimeout      time.Duration
	// Indexer, if non-nil, is called after each memory is persisted so that
	// consolidated memories are discoverable via smart-search.
	Indexer MemoryIndexer
	// Graph, if non-nil, receives extracted entities and relationships
	// (PostgreSQL-backed). Set after construction like Indexer.
	Graph *search.GraphIndex
	// Neo4j, if non-nil, mirrors entity writes from Graph best-effort.
	// Nil when NEO4J_ENABLED=false (ADR-0003).
	Neo4j *graph.Store
}

// NewPipeline wires a Pipeline with reasonable defaults.
func NewPipeline(s *state.Store, pythonURL string, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		Store:            s,
		PythonServiceURL: pythonURL,
		HTTPClient:       &http.Client{Timeout: 30 * time.Second},
		Logger:           logger,
		StepTimeout:      60 * time.Second,
	}
}

// Run executes the pipeline for one session. Returns the produced summary id
// (== session id), the count of memories distilled, and the first non-nil
// error encountered while persisting (best-effort: side effects may have
// occurred even on error).
func (p *Pipeline) Run(ctx context.Context, sessionID string) (memCount int, err error) {
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	log := p.Logger.With("session_id", sessionID, "component", "consolidation")
	log.Info("consolidation.start")

	obs, totalRaw, err := p.fetchObservations(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("fetch observations: %w", err)
	}
	log.Info("consolidation.fetch", "compressed", len(obs), "total_raw", totalRaw)
	if len(obs) == 0 {
		if totalRaw > 0 {
			log.Warn("consolidation.noop", "reason", "no compressed observations yet", "total_raw", totalRaw,
				"hint", "wait for the compression worker to process this session's observations")
		} else {
			log.Info("consolidation.noop", "reason", "session has no observations")
		}
		return 0, nil
	}

	// Step 2: summarise. Python-backed; falls back to a Go-side synthetic
	// summary if /summarize fails.
	log.Info("consolidation.summarize.start", "observations", len(obs))
	summary, err := p.summarise(ctx, sessionID, obs)
	if err != nil {
		log.Warn("consolidation.summarize_failed", "err", err)
		summary = syntheticSummaryFromObservations(sessionID, obs)
	}
	log.Info("consolidation.summarize.done", "title", summary.Title, "decisions", len(summary.KeyDecisions), "concepts", len(summary.Concepts))
	if err := p.persistSummary(ctx, summary); err != nil {
		log.Error("consolidation.persist_summary", "err", err)
		return 0, err
	}

	// Step 3: distil 1+ Memory rows from the summary.
	memories := distilMemories(obs, summary)
	if len(memories) == 0 {
		log.Info("consolidation.noop", "reason", "session summary has no substantive content",
			"files", len(summary.FilesModified), "decisions", len(summary.KeyDecisions), "concepts", len(summary.Concepts))
		return 0, nil
	}
	log.Info("consolidation.distil", "memory_count", len(memories))
	for _, m := range memories {
		if err := p.persistMemory(ctx, m); err != nil {
			log.Warn("consolidation.persist_memory.failed", "memory_id", m.ID, "err", err)
			continue
		}
		log.Info("consolidation.persist_memory.ok", "memory_id", m.ID, "title", m.Title, "tier", m.Tier)
		memCount++
		if p.Indexer != nil {
			text := m.Title + " " + m.Content
			if err := p.Indexer.IndexObservation(ctx, m.ID, m.Project, text); err != nil {
				log.Warn("consolidation.index_memory.failed", "memory_id", m.ID, "err", err)
			} else {
				log.Info("consolidation.index_memory.ok", "memory_id", m.ID)
			}
		}
	}

	// Step 4: extract entities + relationships from each observation and
	// persist to the PostgreSQL graph (and optionally Neo4j). Best-effort.
	if p.Graph != nil {
		p.extractAndPersistEntities(ctx, obs, summary.SessionID, log)
	}

	// Step 5: mark the session completed (idempotent).
	if err := p.Store.MarkSessionEnded(ctx, sessionID); err != nil {
		log.Warn("consolidation.mark_ended", "err", err)
	}

	log.Info("consolidation.done", "memories_persisted", memCount, "observations_processed", len(obs))
	return memCount, nil
}

// extractedEntity matches the Python /extract response entity shape.
type extractedEntity struct {
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`
	Subtype              string   `json:"subtype,omitempty"`
	Confidence           float64  `json:"confidence"`
	SourceObservationIDs []string `json:"sourceObservationIds"`
}

// extractedRelationship matches the Python /extract response relationship shape.
type extractedRelationship struct {
	Source              string  `json:"source"`
	Target              string  `json:"target"`
	Type                string  `json:"type"`
	Confidence          float64 `json:"confidence"`
	SourceObservationID string  `json:"sourceObservationId,omitempty"`
}

type extractResponse struct {
	Entities      []extractedEntity      `json:"entities"`
	Relationships []extractedRelationship `json:"relationships"`
}

// callExtract sends narrative text to Python /extract and returns typed entities
// and relationships. Returns empty result (no error) when Python is unreachable.
func (p *Pipeline) callExtract(ctx context.Context, text, observationID string) (*extractResponse, error) {
	if text == "" {
		return &extractResponse{}, nil
	}
	body, _ := json.Marshal(map[string]string{
		"text":                  text,
		"source_observation_id": observationID,
	})
	url := strings.TrimRight(p.PythonServiceURL, "/") + "/extract"
	reqCtx, cancel := context.WithTimeout(ctx, p.StepTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("extract: status %d: %s", resp.StatusCode, raw)
	}
	var out extractResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// entityKey uniquely identifies an entity within a project for ID lookup.
type entityKey struct{ project, name, typ string }

// extractAndPersistEntities calls /extract per observation and writes the
// resulting entities and relationships to both the PostgreSQL graph and (if
// configured) the Neo4j mirror. The entire step is best-effort: any error is
// logged and the loop continues.
func (p *Pipeline) extractAndPersistEntities(ctx context.Context, obs []*state.ObservationRow, sessionID string, log *slog.Logger) {
	// idMap maps (project+name+type) → PostgreSQL entity ID so we can
	// resolve relationship endpoints across observations.
	idMap := make(map[entityKey]string)

	for _, o := range obs {
		text := o.Narrative
		if text == "" {
			text = o.Title
		}
		result, err := p.callExtract(ctx, text, o.ID)
		if err != nil {
			log.Warn("consolidation.extract_failed", "obs", o.ID, "err", err)
			continue
		}
		log.Debug("consolidation.extract", "obs", o.ID, "entities", len(result.Entities), "relationships", len(result.Relationships))

		for _, e := range result.Entities {
			if e.Name == "" || e.Type == "" {
				continue
			}
			pgEnt, err := p.Graph.UpsertEntity(ctx, o.Project, e.Name, e.Type, e.Subtype, e.Confidence)
			if err != nil {
				log.Warn("consolidation.upsert_entity_failed", "name", e.Name, "err", err)
				continue
			}
			key := entityKey{o.Project, strings.ToLower(e.Name), e.Type}
			idMap[key] = pgEnt.ID

			if linkErr := p.Graph.LinkObservationToEntity(ctx, o.ID, pgEnt.ID); linkErr != nil {
				log.Warn("consolidation.link_entity_failed", "obs", o.ID, "entity", pgEnt.ID, "err", linkErr)
			}

			// Mirror to Neo4j best-effort — use same Postgres-assigned ID so
			// edges connect correctly in both stores (ADR-0003).
			if p.Neo4j != nil {
				if mirrorErr := p.Neo4j.UpsertEntity(ctx, graph.Entity{
					ID:         pgEnt.ID,
					Project:    o.Project,
					Name:       e.Name,
					Type:       e.Type,
					Subtype:    e.Subtype,
					Confidence: e.Confidence,
				}); mirrorErr != nil {
					log.Warn("consolidation.neo4j_upsert_failed", "entity", pgEnt.ID, "err", mirrorErr)
				}
			}
		}

		for _, rel := range result.Relationships {
			srcKey := entityKey{o.Project, strings.ToLower(rel.Source), ""}
			tgtKey := entityKey{o.Project, strings.ToLower(rel.Target), ""}
			srcID := findEntityID(idMap, srcKey)
			tgtID := findEntityID(idMap, tgtKey)
			if srcID == "" || tgtID == "" {
				continue
			}
			if err := p.Graph.AddEdge(ctx, o.Project, search.Edge{
				SourceID:            srcID,
				TargetID:            tgtID,
				Type:                rel.Type,
				Confidence:          rel.Confidence,
				SourceObservationID: o.ID,
			}); err != nil {
				log.Warn("consolidation.add_edge_failed", "src", rel.Source, "tgt", rel.Target, "err", err)
				continue
			}
			if p.Neo4j != nil {
				if mirrorErr := p.Neo4j.AddEdge(ctx, graph.Edge{
					SourceID:            srcID,
					TargetID:            tgtID,
					Type:                rel.Type,
					Confidence:          rel.Confidence,
					SourceObservationID: o.ID,
				}); mirrorErr != nil {
					log.Warn("consolidation.neo4j_edge_failed", "src", srcID, "tgt", tgtID, "err", mirrorErr)
				}
			}
		}
	}
}

// findEntityID looks up an entity by project+name, ignoring type (used for
// relationship resolution where the extracted type may not match exactly).
func findEntityID(m map[entityKey]string, key entityKey) string {
	if id, ok := m[key]; ok {
		return id
	}
	for k, id := range m {
		if k.project == key.project && k.name == key.name {
			return id
		}
	}
	return ""
}

// fetchObservations returns every compressed observation for the session,
// plus the total raw count (compressed + uncompressed) for diagnostics.
// Uncompressed rows are skipped — their narrative would be empty.
func (p *Pipeline) fetchObservations(ctx context.Context, sessionID string) (_ []*state.ObservationRow, totalRaw int, _ error) {
	_ = p.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM observations WHERE session_id = $1`, sessionID,
	).Scan(&totalRaw)

	rows, err := p.Store.DB.QueryContext(ctx, `
        SELECT id FROM observations
        WHERE session_id = $1 AND compressed = 1
        ORDER BY created_at ASC
    `, sessionID)
	if err != nil {
		return nil, totalRaw, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, totalRaw, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, totalRaw, err
	}

	out := make([]*state.ObservationRow, 0, len(ids))
	for _, id := range ids {
		row, err := p.Store.GetObservation(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, row)
	}
	return out, totalRaw, nil
}

// summaryDigest is what we send to Python /summarize.
type summaryDigest struct {
	Title     string   `json:"title"`
	Narrative string   `json:"narrative,omitempty"`
	Concepts  []string `json:"concepts,omitempty"`
	Files     []string `json:"files,omitempty"`
	Facts     []string `json:"facts,omitempty"`
}

// summarise calls POST /summarize on the Python service.
func (p *Pipeline) summarise(ctx context.Context, sessionID string, obs []*state.ObservationRow) (*sessionSummary, error) {
	digests := make([]summaryDigest, 0, len(obs))
	for _, o := range obs {
		d := summaryDigest{Title: o.Title, Narrative: o.Narrative}
		_ = json.Unmarshal([]byte(o.ConceptsJSON), &d.Concepts)
		_ = json.Unmarshal([]byte(o.FilesJSON), &d.Files)
		_ = json.Unmarshal([]byte(o.FactsJSON), &d.Facts)
		digests = append(digests, d)
	}
	body, err := json.Marshal(map[string]any{
		"sessionId":    sessionID,
		"observations": digests,
	})
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(p.PythonServiceURL, "/") + "/summarize"
	reqCtx, cancel := context.WithTimeout(ctx, p.StepTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("summarize: status %d: %s", resp.StatusCode, raw)
	}
	var out sessionSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	// Backfill in case alias mapping in Python returned snake_case fields.
	if out.SessionID == "" {
		out.SessionID = sessionID
	}
	return &out, nil
}

// sessionSummary matches the Python SessionSummary shape (alias-friendly).
type sessionSummary struct {
	SessionID     string   `json:"sessionId"`
	Title         string   `json:"title"`
	Narrative     string   `json:"narrative"`
	KeyDecisions  []string `json:"keyDecisions"`
	FilesModified []string `json:"filesModified"`
	Concepts      []string `json:"concepts"`
}

// syntheticSummaryFromObservations produces a session summary entirely in Go,
// used when /summarize is unreachable. Mirrors the Python synthetic
// summariser's strategy but lighter (no decision sniffing).
func syntheticSummaryFromObservations(sessionID string, obs []*state.ObservationRow) *sessionSummary {
	seenConcepts := map[string]int{}
	seenFiles := map[string]struct{}{}
	narrativeParts := make([]string, 0, len(obs))
	for _, o := range obs {
		if o.Narrative != "" {
			narrativeParts = append(narrativeParts, firstSentence(o.Narrative))
		}
		var concepts, files []string
		_ = json.Unmarshal([]byte(o.ConceptsJSON), &concepts)
		_ = json.Unmarshal([]byte(o.FilesJSON), &files)
		for _, c := range concepts {
			c = strings.ToLower(strings.TrimSpace(c))
			if c == "" {
				continue
			}
			seenConcepts[c]++
		}
		for _, f := range files {
			if f != "" {
				seenFiles[f] = struct{}{}
			}
		}
	}

	// Pick top concepts by frequency.
	type kv struct {
		k string
		v int
	}
	concepts := make([]kv, 0, len(seenConcepts))
	for k, v := range seenConcepts {
		concepts = append(concepts, kv{k, v})
	}
	// Stable sort by frequency desc.
	for i := 0; i < len(concepts); i++ {
		for j := i + 1; j < len(concepts); j++ {
			if concepts[j].v > concepts[i].v {
				concepts[i], concepts[j] = concepts[j], concepts[i]
			}
		}
	}
	topConcepts := make([]string, 0, 10)
	for i, c := range concepts {
		if i >= 10 {
			break
		}
		topConcepts = append(topConcepts, c.k)
	}

	files := make([]string, 0, len(seenFiles))
	for f := range seenFiles {
		files = append(files, f)
	}

	title := "Session"
	if len(topConcepts) > 0 {
		title = "Session on " + topConcepts[0]
	}

	return &sessionSummary{
		SessionID:     sessionID,
		Title:         title,
		Narrative:     strings.Join(narrativeParts, " • "),
		FilesModified: files,
		Concepts:      topConcepts,
	}
}

// firstSentence returns the leading sentence of s (or s if no terminator).
func firstSentence(s string) string {
	idx := strings.IndexAny(s, ".!?")
	if idx == -1 || idx > 200 {
		if len(s) > 200 {
			return s[:200]
		}
		return s
	}
	return s[:idx]
}

// persistSummary writes the session summary into sessions_summary.
func (p *Pipeline) persistSummary(ctx context.Context, s *sessionSummary) error {
	if s == nil {
		return errors.New("persistSummary: nil")
	}
	keyDecisionsJSON, _ := json.Marshal(s.KeyDecisions)
	filesJSON, _ := json.Marshal(s.FilesModified)
	conceptsJSON, _ := json.Marshal(s.Concepts)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := p.Store.DB.ExecContext(ctx, `
        INSERT INTO sessions_summary (session_id, title, narrative, key_decisions, files_modified, concepts, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT(session_id) DO UPDATE SET
            title          = excluded.title,
            narrative      = excluded.narrative,
            key_decisions  = excluded.key_decisions,
            files_modified = excluded.files_modified,
            concepts       = excluded.concepts,
            created_at     = excluded.created_at
    `, s.SessionID, s.Title, s.Narrative,
		string(keyDecisionsJSON), string(filesJSON), string(conceptsJSON), now)
	return err
}

// memoryRow is the row shape persistMemory writes.
type memoryRow struct {
	ID                   string
	Project              string
	Type                 string
	Tier                 string
	Title                string
	Content              string
	Concepts             []string
	Files                []string
	SessionIDs           []string
	SourceObservationIDs []string
}

// distilMemories produces one Memory per session summary plus optional
// per-concept Memories. Real LLM clustering (semantic) is Task 27 / future
// work; this baseline gives us a navigable memory store immediately.
// hasSubstantiveContent returns true if the summary describes work worth
// remembering: at least one file was modified or a key decision was made.
// Concepts alone are not sufficient — an LLM will generate concept tags
// (e.g. "empty_session", "tool_invocations") even for sessions with no real
// work, which would otherwise produce noise memories.
func hasSubstantiveContent(s *sessionSummary) bool {
	return len(s.FilesModified) > 0 || len(s.KeyDecisions) > 0
}

func distilMemories(obs []*state.ObservationRow, summary *sessionSummary) []memoryRow {
	if summary == nil || len(obs) == 0 {
		return nil
	}
	if !hasSubstantiveContent(summary) {
		return nil
	}
	project := obs[0].Project
	files := summary.FilesModified
	concepts := summary.Concepts

	out := []memoryRow{{
		ID:                   newMemoryID(),
		Project:              project,
		Type:                 "workflow",
		Tier:                 "semantic",
		Title:                summary.Title,
		Content:              summary.Narrative,
		Concepts:             concepts,
		Files:                files,
		SessionIDs:           []string{summary.SessionID},
		SourceObservationIDs: collectObsIDs(obs),
	}}

	// Plus a Memory per key decision so they're individually recallable.
	for _, d := range summary.KeyDecisions {
		out = append(out, memoryRow{
			ID:                   newMemoryID(),
			Project:              project,
			Type:                 "architecture",
			Tier:                 "semantic",
			Title:                truncate(d, 120),
			Content:              d,
			Concepts:             concepts,
			Files:                files,
			SessionIDs:           []string{summary.SessionID},
			SourceObservationIDs: collectObsIDs(obs),
		})
	}
	return out
}

func collectObsIDs(obs []*state.ObservationRow) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		out = append(out, o.ID)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// persistMemory writes a memory row.
func (p *Pipeline) persistMemory(ctx context.Context, m memoryRow) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	concepts, _ := json.Marshal(m.Concepts)
	files, _ := json.Marshal(m.Files)
	sessions, _ := json.Marshal(m.SessionIDs)
	sources, _ := json.Marshal(m.SourceObservationIDs)

	_, err := p.Store.DB.ExecContext(ctx, `
        INSERT INTO memories (
            id, project, type, tier, title, content,
            concepts_json, files_json, session_ids_json, source_observation_ids,
            strength, is_latest, created_at, updated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1.0, 1, $11, $12)
    `, m.ID, m.Project, m.Type, m.Tier, m.Title, m.Content,
		string(concepts), string(files), string(sessions), string(sources),
		now, now)
	return err
}

func newMemoryID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "mem-" + hex.EncodeToString(b[:])
}

// SessionEndHandler glue: satisfies api.SessionEndHandler so that the API
// process can invoke the pipeline directly (in-process mode) instead of
// always going through the queue. The worker still consumes JobConsolidate
// for cross-process orchestration.
type SessionEndHandler struct {
	Pipeline *Pipeline
}

// OnSessionEnd kicks the consolidation pipeline for sessionID.
func (h SessionEndHandler) OnSessionEnd(ctx context.Context, sessionID string) error {
	if h.Pipeline == nil {
		return nil
	}
	_, err := h.Pipeline.Run(ctx, sessionID)
	return err
}
