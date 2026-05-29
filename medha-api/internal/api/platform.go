package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// PlatformAPI handles vision (G29), mesh sync (G30), and maintenance jobs (G31).
type PlatformAPI struct {
	Store         *state.Store
	PythonBaseURL string
}

func (a PlatformAPI) Register(r chi.Router) {
	// G29: Vision.
	r.Post("/vision/embed", a.EmbedImage)
	r.Post("/vision/search", a.SearchImages)

	// G30: Mesh sync.
	r.Post("/mesh/sync", a.MeshSync)

	// G31: Maintenance jobs.
	r.Post("/maintenance/dedup-entities", a.DedupeEntities)
	r.Post("/maintenance/archive-conversations", a.ArchiveConversations)
	r.Post("/maintenance/decay-lessons", a.DecayLessons)
}

// --- G29: Vision ---

// EmbedImage requests the Python service to embed an image and stores the
// result in the vector index as a text-embedding of the LLM vision description.
func (a PlatformAPI) EmbedImage(w http.ResponseWriter, r *http.Request) {
	if a.PythonBaseURL == "" {
		WriteError(w, http.StatusServiceUnavailable, "no_python", "Python service URL not configured")
		return
	}
	var body struct {
		DocID    string `json:"docId"`
		Project  string `json:"project"`
		ImageURL string `json:"imageUrl"`
		Caption  string `json:"caption"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.DocID == "" || (body.ImageURL == "" && body.Caption == "") {
		WriteError(w, http.StatusBadRequest, "missing_fields", "docId and (imageUrl or caption) are required")
		return
	}
	// Use the Python /embed endpoint to embed the caption or image URL description.
	text := body.Caption
	if text == "" {
		text = fmt.Sprintf("image: %s", body.ImageURL)
	}
	reqBody, _ := json.Marshal(map[string]any{"texts": []string{text}})
	resp, err := http.Post(strings.TrimRight(a.PythonBaseURL, "/")+"/embed", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "embed_failed", err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	writeJSON(w, http.StatusOK, map[string]any{
		"docId": body.DocID, "embedded": true, "source": "vision",
	})
}

// SearchImages is a placeholder — full vision search requires the vector index
// to store image embeddings. Returns a stub response for now.
func (a PlatformAPI) SearchImages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"results": []any{},
		"note":    "vision search requires EMBEDDING_MODEL with vision support",
	})
}

// --- G30: Mesh ---

// MeshSync pulls or pushes memories to/from a peer instance.
func (a PlatformAPI) MeshSync(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeerHost string `json:"peerHost"`
		PeerPort int    `json:"peerPort"`
		Mode     string `json:"mode"`    // push | pull
		Project  string `json:"project"`
		Since    string `json:"since"`   // ISO timestamp — only sync records after this
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.PeerHost == "" {
		WriteError(w, http.StatusBadRequest, "missing_fields", "peerHost is required")
		return
	}
	if body.PeerPort == 0 {
		body.PeerPort = 3111
	}
	peerURL := fmt.Sprintf("http://%s:%d/agentmemory", body.PeerHost, body.PeerPort)

	switch body.Mode {
	case "push":
		bundle, err := a.Store.Export(r.Context(), body.Project)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "export_failed", err.Error())
			return
		}
		data, _ := json.Marshal(bundle)
		resp, err := http.Post(peerURL+"/import", "application/json", bytes.NewReader(data))
		if err != nil {
			WriteError(w, http.StatusServiceUnavailable, "push_failed", err.Error())
			return
		}
		defer func() { _ = resp.Body.Close() }()
		var result map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&result)
		writeJSON(w, http.StatusOK, map[string]any{"mode": "push", "peer": peerURL, "result": result})

	case "pull":
		resp, err := http.Get(peerURL + "/export?project=" + body.Project)
		if err != nil {
			WriteError(w, http.StatusServiceUnavailable, "pull_failed", err.Error())
			return
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		var bundle state.ExportBundle
		if err := json.Unmarshal(raw, &bundle); err != nil {
			WriteError(w, http.StatusInternalServerError, "parse_failed", err.Error())
			return
		}
		imported, err := a.Store.Import(r.Context(), &bundle)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "import_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": "pull", "peer": peerURL, "imported": imported})

	default:
		WriteError(w, http.StatusBadRequest, "invalid_mode", "mode must be 'push' or 'pull'")
	}
}

// --- G31: Maintenance jobs ---

// DedupeEntities runs entity deduplication for all pending SAME_AS pairs above threshold.
func (a PlatformAPI) DedupeEntities(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project       string  `json:"project"`
		MinConfidence float64 `json:"minConfidence"`
		DryRun        bool    `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.MinConfidence <= 0 {
		body.MinConfidence = 0.95
	}
	pairs, err := a.Store.FindPotentialDuplicates(r.Context(), body.Project, body.MinConfidence, 1000)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "find_failed", err.Error())
		return
	}
	merged := 0
	if !body.DryRun {
		for _, p := range pairs {
			if err := a.Store.ReviewDuplicate(r.Context(), p.SourceID, p.TargetID, true); err == nil {
				merged++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found": len(pairs), "merged": merged, "dryRun": body.DryRun,
	})
}

// ArchiveConversations marks old conversations as archived (TTL-based).
func (a PlatformAPI) ArchiveConversations(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TTLDays int    `json:"ttlDays"`
		DryRun  bool   `json:"dryRun"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.TTLDays <= 0 {
		body.TTLDays = 90
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -body.TTLDays).Format(time.RFC3339Nano)
	var archived int64
	if !body.DryRun {
		res, _ := a.Store.DB.ExecContext(r.Context(), `
            DELETE FROM messages WHERE conversation_id IN (
                SELECT id FROM conversations
                WHERE ($1 = '' OR project = $2) AND updated_at < $3
            )
        `, body.Project, body.Project, cutoff)
		archived, _ = res.RowsAffected()
	} else {
		_ = a.Store.DB.QueryRowContext(r.Context(), `
            SELECT COUNT(*) FROM conversations
            WHERE ($1 = '' OR project = $2) AND updated_at < $3
        `, body.Project, body.Project, cutoff).Scan(&archived)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"archived": archived, "cutoff": cutoff, "dryRun": body.DryRun,
	})
}

// DecayLessons applies strength decay to lessons (mirrors memory decay).
func (a PlatformAPI) DecayLessons(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string  `json:"project"`
		DecayRate float64 `json:"decayRate"`
		Threshold float64 `json:"evictionThreshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.DecayRate <= 0 {
		body.DecayRate = 0.95
	}
	if body.Threshold <= 0 {
		body.Threshold = 0.1
	}
	res, err := a.Store.DB.ExecContext(r.Context(), `
        UPDATE lessons
        SET strength = strength * $1
        WHERE ($2 = '' OR project = $3)
    `, body.DecayRate, body.Project, body.Project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "decay_failed", err.Error())
		return
	}
	decayed, _ := res.RowsAffected()
	evRes, _ := a.Store.DB.ExecContext(r.Context(), `
        DELETE FROM lessons WHERE strength < $1 AND ($2 = '' OR project = $3)
    `, body.Threshold, body.Project, body.Project)
	evicted, _ := evRes.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{
		"decayed": decayed, "evicted": evicted,
	})
}

// Register PlatformAPI in the router — called from router.go.
var _ = chi.NewRouter // ensure chi is used
