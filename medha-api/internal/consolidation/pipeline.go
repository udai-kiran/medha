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

	"github.com/udai-kiran/medha/internal/state"
)

// Pipeline runs the SessionEnd consolidation DAG:
//
//	1. Fetch CompressedObservation rows for the session.
//	2. POST /summarize on Python → SessionSummary.
//	3. POST /extract on each observation → entities + relationships.
//	4. Persist SessionSummary, link observations to entities, build edges,
//	   distil memories.
//
// The pipeline is best-effort: each step is independent and may fail
// without aborting the others. Python unreachable → fall back to a synthetic
// session summary built in Go from the raw observation rows.
type Pipeline struct {
	Store               *state.Store
	PythonServiceURL    string
	HTTPClient          *http.Client
	Logger              *slog.Logger
	StepTimeout         time.Duration
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

	obs, err := p.fetchObservations(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("fetch observations: %w", err)
	}
	if len(obs) == 0 {
		log.Info("consolidation.noop", "reason", "no observations")
		return 0, nil
	}

	// Step 2: summarise. Python-backed; falls back to a Go-side synthetic
	// summary if /summarize fails.
	summary, err := p.summarise(ctx, sessionID, obs)
	if err != nil {
		log.Warn("consolidation.summarize_failed", "err", err)
		summary = syntheticSummaryFromObservations(sessionID, obs)
	}
	if err := p.persistSummary(ctx, summary); err != nil {
		log.Error("consolidation.persist_summary", "err", err)
		return 0, err
	}

	// Step 3: distil 1+ Memory rows from the summary.
	memories := distilMemories(obs, summary)
	for _, m := range memories {
		if err := p.persistMemory(ctx, m); err != nil {
			log.Warn("consolidation.persist_memory", "memory_id", m.ID, "err", err)
			continue
		}
		memCount++
	}

	// Step 4: mark the session completed (idempotent).
	if err := p.Store.MarkSessionEnded(ctx, sessionID); err != nil {
		log.Warn("consolidation.mark_ended", "err", err)
	}

	log.Info("consolidation.done", "memories", memCount, "observations", len(obs))
	return memCount, nil
}

// fetchObservations returns every compressed observation for the session.
// Uncompressed rows are skipped — their narrative would be empty.
func (p *Pipeline) fetchObservations(ctx context.Context, sessionID string) ([]*state.ObservationRow, error) {
	rows, err := p.Store.DB.QueryContext(ctx, `
        SELECT id FROM observations
        WHERE session_id = ? AND compressed = 1
        ORDER BY created_at ASC
    `, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*state.ObservationRow, 0, len(ids))
	for _, id := range ids {
		row, err := p.Store.GetObservation(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, row)
	}
	return out, nil
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
        VALUES (?, ?, ?, ?, ?, ?, ?)
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
func distilMemories(obs []*state.ObservationRow, summary *sessionSummary) []memoryRow {
	if summary == nil || len(obs) == 0 {
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
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1.0, 1, ?, ?)
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
