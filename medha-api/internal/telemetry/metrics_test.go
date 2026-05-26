package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetrics_Exposition(t *testing.T) {
	m := NewMetrics()
	m.ObservationsTotal.WithLabelValues("p", "post_tool_use", "201").Inc()
	m.DedupHits.WithLabelValues("p").Inc()
	m.SearchLatency.WithLabelValues("hybrid").Observe(0.123)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"agent_mem_observations_total",
		"agent_mem_dedup_hits_total",
		"agent_mem_search_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metric %q missing in body", want)
		}
	}
}

func TestMetrics_HelpAndType(t *testing.T) {
	m := NewMetrics()
	m.ObservationsTotal.WithLabelValues("p", "user_prompt", "201").Inc()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "# HELP agent_mem_observations_total") {
		t.Error("missing HELP comment")
	}
	if !strings.Contains(body, "# TYPE agent_mem_observations_total counter") {
		t.Error("missing TYPE comment")
	}
}
