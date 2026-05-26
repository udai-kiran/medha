package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/udai-kiran/medha/internal/config"
)

func TestHealth_DefaultDegraded(t *testing.T) {
	cfg := config.FromEnv()
	cfg.Neo4jEnabled = false

	req := httptest.NewRequest(http.MethodGet, "/agentmemory/health", nil)
	w := httptest.NewRecorder()
	Health(cfg)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "degraded" {
		t.Errorf("Status = %q, want degraded (neo4j disabled)", resp.Status)
	}

	var sawNeo4j bool
	for _, c := range resp.Components {
		if c.Name == "neo4j" {
			sawNeo4j = true
			if c.Status != "degraded" {
				t.Errorf("neo4j component status = %q, want degraded", c.Status)
			}
		}
	}
	if !sawNeo4j {
		t.Error("expected a neo4j component entry when disabled")
	}
}

func TestHealth_OKWhenNeo4jEnabledProbeOK(t *testing.T) {
	cfg := config.FromEnv()
	cfg.Neo4jEnabled = true
	probe := func() ComponentStatus { return ComponentStatus{Name: "neo4j", Status: "ok"} }

	req := httptest.NewRequest(http.MethodGet, "/agentmemory/health", nil)
	w := httptest.NewRecorder()
	Health(cfg, probe)(w, req)

	var resp HealthResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Status != "ok" {
		t.Errorf("Status = %q, want ok", resp.Status)
	}
}
