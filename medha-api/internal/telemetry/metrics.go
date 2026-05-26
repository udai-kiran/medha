package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Metrics groups every Prometheus collector the Go service exports.
// One instance per process; constructed in main and passed to handlers
// that emit counters. Tests get a fresh, isolated registry per call.
type Metrics struct {
	Registry *prometheus.Registry

	// Capture-path counters.
	ObservationsTotal     *prometheus.CounterVec   // labels: project, hook_type, status
	DedupHits             *prometheus.CounterVec   // labels: project
	PrivacyRedactions     *prometheus.CounterVec   // labels: pattern

	// Search-path histograms.
	SearchLatency *prometheus.HistogramVec // labels: mode

	// Consolidation + decay counters.
	ConsolidationRuns   *prometheus.CounterVec   // labels: outcome
	DecayMemoriesEvicted prometheus.Counter
	DecayObservationsEvicted *prometheus.CounterVec // labels: tier

	// LLM + embedding accounting (for cost dashboards).
	LLMCallsTotal   *prometheus.CounterVec // labels: provider, op
	EmbedCallsTotal *prometheus.CounterVec // labels: provider
}

// NewMetrics wires the registry. Safe to call once per process.
func NewMetrics() *Metrics {
	r := prometheus.NewRegistry()
	m := &Metrics{Registry: r}

	m.ObservationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_observations_total",
		Help: "Observations accepted by POST /observe, grouped by hook type and outcome.",
	}, []string{"project", "hook_type", "status"})

	m.DedupHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_dedup_hits_total",
		Help: "Observations dropped as duplicates within the dedup window.",
	}, []string{"project"})

	m.PrivacyRedactions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_privacy_redactions_total",
		Help: "Privacy filter hits, by pattern name.",
	}, []string{"pattern"})

	m.SearchLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "agent_mem_search_duration_seconds",
		Help:    "Smart-search latency, by mode.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
	}, []string{"mode"})

	m.ConsolidationRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_consolidation_runs_total",
		Help: "Consolidation pipeline executions, by outcome.",
	}, []string{"outcome"})

	m.DecayMemoriesEvicted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "agent_mem_decay_memories_evicted_total",
		Help: "Memories hard-evicted by the decay job.",
	})

	m.DecayObservationsEvicted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_decay_observations_evicted_total",
		Help: "Observations evicted by tier (working / episodic).",
	}, []string{"tier"})

	m.LLMCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_llm_calls_total",
		Help: "LLM API calls, by provider and operation (compress, summarize, extract).",
	}, []string{"provider", "op"})

	m.EmbedCallsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_mem_embed_calls_total",
		Help: "Embedding API calls, by provider.",
	}, []string{"provider"})

	r.MustRegister(
		m.ObservationsTotal, m.DedupHits, m.PrivacyRedactions,
		m.SearchLatency, m.ConsolidationRuns,
		m.DecayMemoriesEvicted, m.DecayObservationsEvicted,
		m.LLMCallsTotal, m.EmbedCallsTotal,
	)
	return m
}

// Handler returns an http.Handler exposing the registry as Prometheus
// exposition format. Mount under /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
