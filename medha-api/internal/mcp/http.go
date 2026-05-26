package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// HTTPHandler returns an http.Handler that lets clients invoke MCP methods
// over HTTP POST (one JSON-RPC request body → one response body). This is
// the "HTTP proxy" leg of the MCP server — the agent_mem REST surface lives
// at /agentmemory/* separately; MCP-over-HTTP is a thin shim for clients
// that can't spawn a stdio process.
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// 1MB cap is generous for MCP requests; raise via reverse proxy if needed.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSONRPCError(w, nil, ErrParseError, err.Error())
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		resp := s.handle(ctx, bytes.TrimSpace(body))
		if resp == nil {
			// Notification — return 204 to signal "no body".
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are 200 with error in body
	_ = json.NewEncoder(w).Encode(Response{
		JSONRPC: "2.0", ID: id,
		Error: &Error{Code: code, Message: msg},
	})
}
