package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// MemoryAPI groups handlers for /agentmemory/memories, /remember, /forget.
type MemoryAPI struct {
	Store         *state.Store
	PythonBaseURL string
	// Indexer, if non-nil, puts user memories into the FTS index so they appear
	// in smart-search and recall-summary results with the "user" provenance boost.
	Indexer IndexBus
}

// Register attaches memory routes.
func (a MemoryAPI) Register(r chi.Router) {
	r.Get("/memories", a.List)
	r.Get("/memory/{id}", a.Get)
	r.Post("/remember", a.Remember)
	r.Post("/forget", a.Forget)
}

// RememberRequest is the NLQ entry point for user-authored memories.
// Only `content` is required — the user writes what they know in plain language.
// `type` and `concepts` are optional overrides; when absent they are derived by
// calling the Python /extract-memory endpoint. Title is no longer part of the API.
type RememberRequest struct {
	Project  string   `json:"project,omitempty"`
	Content  string   `json:"content"`           // NLQ — required
	Type     string   `json:"type,omitempty"`    // optional override
	Tier     string   `json:"tier,omitempty"`    // optional override
	Concepts []string `json:"concepts,omitempty"` // optional override
	Files    []string `json:"files,omitempty"`    // optional
}

// RememberResponse is returned on successful memory creation.
type RememberResponse struct {
	MemoryID string `json:"memoryId"`
	// Extracted fields echoed back so the caller can inspect what the LLM derived.
	Type     string   `json:"type"`
	Concepts []string `json:"concepts"`
}

// extractedMemory is the shape returned by Python /extract-memory.
type extractedMemory struct {
	Type              string   `json:"type"`
	NormalizedContent string   `json:"normalized_content"`
	Concepts          []string `json:"concepts"`
	Facts             []string `json:"facts"`
}

// extractMemory calls Python /extract-memory to derive structure from NLQ text.
// Returns a best-effort result on any error (never fails the remember path).
func (a MemoryAPI) extractMemory(r *http.Request, content string) extractedMemory {
	fallback := extractedMemory{
		Type:              "fact",
		NormalizedContent: content,
	}
	if a.PythonBaseURL == "" {
		return fallback
	}
	body, _ := json.Marshal(map[string]string{"content": content})
	url := strings.TrimRight(a.PythonBaseURL, "/") + "/extract-memory"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fallback
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fallback
	}
	raw, _ := io.ReadAll(resp.Body)
	var out extractedMemory
	if err := json.Unmarshal(raw, &out); err != nil || out.Type == "" {
		return fallback
	}
	return out
}

// Remember persists a user-authored memory from a natural language query.
func (a MemoryAPI) Remember(w http.ResponseWriter, r *http.Request) {
	var req RememberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "content is required")
		return
	}

	memType := req.Type
	concepts := req.Concepts
	content := req.Content

	// Derive structure via LLM when not explicitly provided.
	if memType == "" {
		extracted := a.extractMemory(r, content)
		memType = extracted.Type
		if extracted.NormalizedContent != "" {
			content = extracted.NormalizedContent
		}
		if len(concepts) == 0 {
			concepts = extracted.Concepts
		}
	}

	tier := req.Tier
	if tier == "" {
		tier = state.TierSemantic
	}

	// Title is derived from content (first 80 chars) — kept for the DB column
	// but not exposed in the API request shape.
	title := content
	if len(title) > 80 {
		if idx := strings.LastIndex(title[:80], " "); idx > 40 {
			title = title[:idx]
		} else {
			title = title[:80]
		}
	}

	id := "mem-" + randHex(8)
	row := &state.MemoryRow{
		ID:         id,
		Project:    req.Project,
		Type:       memType,
		Tier:       tier,
		Provenance: state.ProvenanceUser,
		Title:      title,
		Content:    content,
		Concepts:   concepts,
		Files:      req.Files,
		Strength:   1.0,
	}
	if err := a.Store.InsertMemory(r.Context(), row); err != nil {
		WriteError(w, http.StatusInternalServerError, "persist_failed", err.Error())
		return
	}
	// Index the user memory so it appears in smart-search/recall-summary.
	// Carries the "user" provenance label for priority boosting. Best-effort.
	if a.Indexer != nil {
		indexText := title + " " + content + " " + strings.Join(concepts, " ")
		_ = a.Indexer.IndexMemory(r.Context(), id, req.Project, indexText, state.ProvenanceUser)
	}
	writeJSON(w, http.StatusCreated, RememberResponse{
		MemoryID: id,
		Type:     memType,
		Concepts: concepts,
	})
}

// Get returns a single memory by id; marks it as retrieved (for decay reinforcement).
func (a MemoryAPI) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := a.Store.GetMemory(r.Context(), id)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "memory not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	_ = a.Store.MarkRetrieved(r.Context(), []string{id})
	writeJSON(w, http.StatusOK, m)
}

// List returns memories filtered by project + tier; ordered by strength.
func (a MemoryAPI) List(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	tier := r.URL.Query().Get("tier")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	rows, err := a.Store.ListMemoriesByTier(r.Context(), project, tier, limit)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	// Mark all returned memories as retrieved.
	ids := make([]string, len(rows))
	for i, m := range rows {
		ids[i] = m.ID
	}
	_ = a.Store.MarkRetrieved(r.Context(), ids)
	writeJSON(w, http.StatusOK, map[string]any{"memories": rows})
}

// ForgetRequest deletes a memory by id and records the audit entry.
type ForgetRequest struct {
	MemoryID string `json:"memoryId"`
	Actor    string `json:"actor,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Forget deletes a memory and writes the action to the audit log.
func (a MemoryAPI) Forget(w http.ResponseWriter, r *http.Request) {
	var req ForgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	if req.MemoryID == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "memoryId required")
		return
	}
	if req.Actor == "" {
		req.Actor = "anonymous"
	}
	payload, _ := json.Marshal(map[string]string{"reason": req.Reason})
	// Audit first; then delete.
	if _, err := a.Store.DB.ExecContext(r.Context(),
		`INSERT INTO audit_log (timestamp, actor, action, target_type, target_id, payload_json)
         VALUES (NOW(), $1, 'delete', 'memory', $2, $3)`,
		req.Actor, req.MemoryID, string(payload),
	); err != nil {
		WriteError(w, http.StatusInternalServerError, "audit_failed", err.Error())
		return
	}
	if err := a.Store.DeleteMemory(r.Context(), req.MemoryID); err != nil {
		WriteError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
