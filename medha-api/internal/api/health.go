package api

import (
	"net/http"

	"github.com/udai-kiran/medha/internal/config"
)

// Version is overridden at build time via -ldflags; default is "dev".
var Version = "dev"

// ComponentStatus describes a single subsystem's liveness.
type ComponentStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`            // ok | degraded | down
	Message string `json:"message,omitempty"` // optional reason
}

// HealthResponse is what /agentmemory/health and /health return.
type HealthResponse struct {
	Status     string            `json:"status"` // ok | degraded | down
	Version    string            `json:"version"`
	Components []ComponentStatus `json:"components"`
}

// Health returns a handler that reports overall + per-component status.
// Component probes are pluggable so later tasks (state, queue, neo4j) can
// register richer checks without touching this file.
func Health(cfg *config.Config, probes ...func() ComponentStatus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		components := make([]ComponentStatus, 0, len(probes)+1)
		// Always-present baseline component.
		components = append(components, ComponentStatus{Name: "api", Status: "ok"})

		// Neo4j is optional (ADR-0003). Surface its disabled state explicitly.
		if !cfg.Neo4jEnabled {
			components = append(components, ComponentStatus{
				Name:    "neo4j",
				Status:  "degraded",
				Message: "disabled (NEO4J_ENABLED=false); PostgreSQL-only mode",
			})
		}

		for _, p := range probes {
			components = append(components, p())
		}

		overall := "ok"
		for _, c := range components {
			if c.Status == "down" {
				overall = "down"
				break
			}
			if c.Status == "degraded" && overall == "ok" {
				overall = "degraded"
			}
		}

		writeJSON(w, http.StatusOK, HealthResponse{
			Status:     overall,
			Version:    Version,
			Components: components,
		})
	}
}
