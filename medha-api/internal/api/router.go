// Package api wires the Chi router and HTTP handlers. Later milestones add
// handlers under /agentmemory (public, agent-facing) and /internal
// (service-to-service callbacks).
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/telemetry"
)

// RouterDeps lets later milestones inject real implementations of the
// capture/search side. The zero value gives a usable router (health-only)
// so the M0 skeleton path still works.
type RouterDeps struct {
	Observe       ObserveDeps        // Task 8  — set Store/Deduper/Enqueuer/Broadcaster/SessionEnd
	Search        SearchDeps         // Task 18 — set Hybrid/Store
	IndexBus      IndexBus           // Task 13 callback — notifies search indexes when a compression lands
	MCP           http.Handler       // Task 26 — optional MCP-over-HTTP proxy
	Metrics       *telemetry.Metrics // Task 29 — Prometheus registry
	AuthSecret    string             // Task 33 — Bearer token; empty disables auth
	RateLimiter   *RateLimiter       // Task 33 — nil disables rate limiting
	PythonBaseURL string             // G29 — for vision embedding proxy
}

// NewRouter builds the API router. Keep this function free of business
// logic — it should only register middleware and routes from sibling files.
func NewRouter(cfg *config.Config, deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	r.Use(requestID)
	r.Use(withLogger)
	r.Use(cors)
	r.Use(recoverer)
	r.Use(requestLog)

	// Infra routes (no /agentmemory prefix so generic probes find them).
	r.Get("/health", Health(cfg))
	r.Get("/agentmemory/health", Health(cfg))
	if deps.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", deps.Metrics.Handler())
	}

	// Public, agent-facing routes. Each task's API surface registers itself
	// here so the router file stays a directory rather than a kitchen sink.
	r.Route("/agentmemory", func(r chi.Router) {
		// Auth + rate limiting only on /agentmemory routes — /health and
		// /metrics stay open so probes don't need credentials.
		r.Use(BearerAuth(deps.AuthSecret))
		r.Use(deps.RateLimiter.Middleware())
		if deps.Observe.Store != nil {
			r.Post("/observe", ObserveHandler(deps.Observe))
			SessionAPI{Store: deps.Observe.Store, SessionEnd: deps.Observe.SessionEnd}.Register(r)
			MemoryAPI{Store: deps.Observe.Store}.Register(r)
			ObservationsAPI{Store: deps.Observe.Store}.Register(r)
			InternalAPI{Store: deps.Observe.Store, IndexBus: deps.IndexBus}.RegisterPublic(r)
			OrchestrationAPI{Store: deps.Observe.Store}.Register(r)
			TeamAPI{Store: deps.Observe.Store}.Register(r)
			ProfileAPI{Store: deps.Observe.Store}.Register(r)
			PatternsAPI{Store: deps.Observe.Store}.Register(r)
			TimelineAPI{Store: deps.Observe.Store}.Register(r)
			ExportAPI{Store: deps.Observe.Store}.Register(r)
			DiagnoseAPI{Store: deps.Observe.Store}.Register(r)
			ConversationsAPI{Store: deps.Observe.Store}.Register(r)
			PreferencesAPI{Store: deps.Observe.Store}.Register(r)
			FactsAPI{Store: deps.Observe.Store}.Register(r)
			TracesAPI{Store: deps.Observe.Store}.Register(r)
			EntitiesAPI{Store: deps.Observe.Store}.Register(r)
			SlotsAPI{Store: deps.Observe.Store}.Register(r)
			GovernanceAPI{Store: deps.Observe.Store}.Register(r)
			AdvancedRetrievalAPI{Store: deps.Observe.Store}.Register(r)
			PlatformAPI{Store: deps.Observe.Store, PythonBaseURL: deps.PythonBaseURL}.Register(r)
			ContextAPI{Store: deps.Observe.Store}.Register(r)
		}
		if deps.Search.Hybrid != nil {
			r.Post("/smart-search", SmartSearchHandler(deps.Search))
			r.Post("/search", SmartSearchHandler(deps.Search))
		}
		if deps.MCP != nil {
			// Streamable HTTP MCP — accepts GET (SSE), POST (requests), DELETE (session close).
			r.Handle("/mcp", deps.MCP)
		}
	})

	// Internal service-to-service routes (Python → Go callbacks, etc.).
	r.Route("/internal", func(r chi.Router) {
		if deps.Observe.Store != nil {
			InternalAPI{Store: deps.Observe.Store, IndexBus: deps.IndexBus}.RegisterInternal(r)
		}
	})

	return r
}
