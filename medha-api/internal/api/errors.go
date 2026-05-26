package api

import (
	"encoding/json"
	"net/http"
)

// ErrorEnvelope is the single JSON shape every error response uses.
// Fallback signals whether a degraded result was returned (e.g. synthetic
// compression after an LLM timeout).
type ErrorEnvelope struct {
	Error    string `json:"error"`
	Message  string `json:"message"`
	Fallback bool   `json:"fallback,omitempty"`
}

// WriteError serialises err as JSON with the given HTTP status.
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorEnvelope{Error: code, Message: msg})
}

// WriteJSON writes v as JSON at the given status. Returns nothing because
// there is no useful recovery if the response stream is already broken.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
