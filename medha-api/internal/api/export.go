package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/state"
)

// ExportAPI groups export/import/snapshot handlers.
type ExportAPI struct {
	Store *state.Store
}

func (a ExportAPI) Register(r chi.Router) {
	r.Get("/export", a.Export)
	r.Post("/import", a.Import)
	r.Post("/import-jsonl", a.ImportJSONL)
	r.Post("/snapshot/create", a.CreateSnapshot)
	r.Get("/snapshot/{id}", a.GetSnapshot)
}

func (a ExportAPI) Export(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	bundle, err := a.Store.Export(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "export_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bundle)
}

func (a ExportAPI) Import(w http.ResponseWriter, r *http.Request) {
	var bundle state.ExportBundle
	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	n, err := a.Store.Import(r.Context(), &bundle)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "import_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

func (a ExportAPI) ImportJSONL(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	sessionID := r.URL.Query().Get("sessionId")

	body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20)) // 50 MB limit
	if err != nil {
		WriteError(w, http.StatusBadRequest, "read_failed", err.Error())
		return
	}
	var lines []string
	scanner := bufio.NewScanner(r.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	// Re-scan from the already-read body.
	scanner2 := bufio.NewScanner(newByteReader(body))
	scanner2.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner2.Scan() {
		lines = append(lines, scanner2.Text())
	}
	_ = scanner

	n, err := a.Store.ImportJSONL(r.Context(), project, sessionID, lines)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "import_jsonl_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

func (a ExportAPI) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	snap, err := a.Store.CreateSnapshot(r.Context(), project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "snapshot_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"snapshotId": snap.ID, "project": snap.Project, "createdAt": snap.CreatedAt,
	})
}

func (a ExportAPI) GetSnapshot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snap, err := a.Store.GetSnapshot(r.Context(), id)
	if err == state.ErrNotFound {
		WriteError(w, http.StatusNotFound, "not_found", "snapshot not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "fetch_failed", err.Error())
		return
	}
	// Parse and re-emit the bundle JSON.
	var bundle state.ExportBundle
	if err := json.Unmarshal([]byte(snap.BundleJSON), &bundle); err != nil {
		WriteError(w, http.StatusInternalServerError, "parse_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshotId": snap.ID, "createdAt": snap.CreatedAt, "bundle": bundle})
}

// newByteReader wraps a byte slice for re-reading.
type byteReader struct {
	data []byte
	pos  int
}

func newByteReader(b []byte) io.Reader {
	return &byteReader{data: b}
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
