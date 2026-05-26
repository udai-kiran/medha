// Command api is the agent_mem Go HTTP service.
// It exposes the public /agentmemory REST surface on :3111 and a viewer
// placeholder on :3113 (real implementation in Task 28).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/udai-kiran/medha/internal/api"
	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/consolidation"
	"github.com/udai-kiran/medha/internal/dedup"
	"github.com/udai-kiran/medha/internal/graph"
	"github.com/udai-kiran/medha/internal/mcp"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/telemetry"
	"github.com/udai-kiran/medha/internal/viewer"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "exit 0 if /health returns 200, else exit 1 (for container probes)")
	flag.Parse()

	cfg := config.FromEnv()
	logger := telemetry.NewLogger(cfg.LogLevel)

	if *healthcheck {
		os.Exit(runHealthcheck(cfg))
	}

	if err := cfg.Validate(); err != nil {
		logger.Error("config.invalid", "err", err)
		os.Exit(2)
	}

	// Wrap startup logger onto root context so handlers inherit it.
	rootCtx := telemetry.WithLogger(context.Background(), logger)

	// State + capture-path collaborators. Queue (Task 12) and viewer (Task 28)
	// remain NoOp until those tasks land; observations are still stored and
	// served — they just don't trigger compression or live broadcasts.
	store, err := state.Open(rootCtx, state.Options{
		Host:     cfg.PostgresHost,
		Port:     cfg.PostgresPort,
		User:     cfg.PostgresUser,
		Password: cfg.PostgresPassword,
		Database: cfg.PostgresDB,
		SSLMode:  cfg.PostgresSSLMode,
	})
	if err != nil {
		logger.Error("state.open", "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	logger.Info("state.ready", "db", cfg.PostgresDB, "host", cfg.PostgresHost, "schema_version", store.SchemaVersion)

	// Async queue: in-memory by default (ADR-0001 / NFR-24). The API process
	// publishes here; the cmd/worker process consumes. In the same-process
	// dev setup we share the queue via this variable.
	queue := consolidation.NewMemoryQueue(256, consolidation.RetryPolicy{Max: 3})
	defer func() { _ = queue.Close() }()
	enq := consolidation.NewEnqueuer(queue)

	// Search engines: BM25 + vector + graph. The vector index talks to the
	// Python /embed endpoint; if Python is down, vector mode degrades to no-op
	// (the hybrid orchestrator silently skips empty results).
	bm25, err := search.NewBM25(rootCtx, store)
	if err != nil {
		logger.Error("search.bm25", "err", err)
		os.Exit(1)
	}
	vec, err := search.NewVectorIndex(rootCtx, store, &search.PythonEmbedder{BaseURL: cfg.PythonServiceURL})
	if err != nil {
		logger.Error("search.vector", "err", err)
		os.Exit(1)
	}
	graphIdx := search.NewGraphIndex(store)
	hybrid := &search.Hybrid{
		BM25: bm25, Vector: vec, Graph: graphIdx,
		K:           60,
		PerGroupCap: 3,
		LookupGroup: func(ctx context.Context, id string) string {
			row, err := store.GetObservation(ctx, id)
			if err != nil || row == nil {
				return ""
			}
			return row.SessionID
		},
	}

	// Neo4j: optional. Health reports degraded if disabled or unreachable.
	var neo4jStore *graph.Store
	if cfg.Neo4jEnabled {
		gs, err := graph.Open(rootCtx, graph.Config{
			URI:      cfg.Neo4jURI,
			Username: cfg.Neo4jUsername,
			Password: cfg.Neo4jPassword,
			Logger:   logger,
		})
		if err != nil {
			logger.Warn("neo4j.open_failed", "err", err)
		} else {
			neo4jStore = gs
			logger.Info("neo4j.ready", "uri", cfg.Neo4jURI)
			defer func() { _ = neo4jStore.Close(context.Background()) }()
		}
	}
	_ = neo4jStore // wired into Task 30 enrichment + health probe

	// Consolidation pipeline: SessionEnd → summarise → distil → persist.
	consolPipeline := consolidation.NewPipeline(store, cfg.PythonServiceURL, logger)

	// Nightly decay job — reads tuning constants from config (ADR-0002).
	if cfg.LessonDecayEnabled {
		decayCfg := consolidation.DecayConfig{
			RatePerDay:               cfg.DecayRatePerDay,
			EvictionThreshold:        cfg.DecayEvictionThreshold,
			WorkingTTL:               24 * time.Hour,
			EpisodicTTL:              7 * 24 * time.Hour,
			ReinforcementOnRetrieval: 0.05,
		}
		decayEngine := consolidation.NewDecayEngine(store, decayCfg, logger)
		decayScheduler := consolidation.NewScheduler(decayEngine, 24*time.Hour, logger)
		go decayScheduler.Start(rootCtx)
	}

	// IndexBus glue: when Python posts back a compression, fan the
	// re-indexing out to BM25 + vector + graph. Keeping this in main.go avoids
	// circular imports between api and search.
	indexBus := indexBusFunc(func(ctx context.Context, observationID, project, text string) error {
		var firstErr error
		if err := bm25.Index(ctx, observationID, project, text); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := vec.Index(ctx, observationID, project, text); err != nil && firstErr == nil {
			logger.Warn("index.vector_failed", "obs", observationID, "err", err)
			// vector index is best-effort (Python may be down); do not fail
		}
		return firstErr
	})

	// Real-time viewer hub (Task 28). The capture path broadcasts observations
	// here; the WebSocket dashboard fans them out to subscribed clients.
	viewerHub := viewer.NewHub(logger)

	// Prometheus metrics — exported at /metrics.
	metrics := telemetry.NewMetrics()

	// MCP-over-HTTP proxy: clients that can't spawn cmd/mcp talk JSON-RPC
	// to /agentmemory/mcp. Same tool surface as the stdio binary.
	mcpServer := mcp.NewServer("agent_mem", "0.1.0", logger)
	mcp.RegisterMemoryTools(mcpServer, mcp.MemoryToolsDeps{Store: store, Search: hybrid})
	mcp.RegisterMemoryResources(mcpServer, mcp.MemoryToolsDeps{Store: store, Search: hybrid})
	mcp.RegisterMemoryPrompts(mcpServer)

	router := api.NewRouter(cfg, api.RouterDeps{
		Observe: api.ObserveDeps{
			Store:       store,
			Deduper:     dedup.NewWindow(5 * time.Minute),
			Enqueuer:    enq,
			Broadcaster: viewerHub,
			SessionEnd:  consolidation.SessionEndHandler{Pipeline: consolPipeline},
		},
		Search:      api.SearchDeps{Hybrid: hybrid, Store: store},
		IndexBus:    indexBus,
		MCP:         mcpServer.HTTPHandler(),
		Metrics:     metrics,
		AuthSecret:  cfg.AgentMemorySecret,
		RateLimiter: api.NewRateLimiter(120, time.Minute), // 120 req/min/client
	})

	apiSrv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return rootCtx },
	}

	// Viewer: WebSocket /stream, SSE /events, and an HTML dashboard at /.
	viewerSrv := &http.Server{
		Addr:              cfg.ViewerAddr(),
		Handler:           viewer.New(viewerHub, logger),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return rootCtx },
	}

	go func() {
		logger.Info("api.listen", "addr", cfg.Addr())
		if err := apiSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api.listen.failed", "err", err)
			os.Exit(1)
		}
	}()
	go func() {
		logger.Info("viewer.listen", "addr", cfg.ViewerAddr())
		if err := viewerSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("viewer.listen.failed", "err", err)
		}
	}()

	// Block until SIGINT / SIGTERM, then drain.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	logger.Info("shutdown.begin", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	var shutdownErr error
	if err := apiSrv.Shutdown(shutdownCtx); err != nil {
		shutdownErr = err
	}
	if err := viewerSrv.Shutdown(shutdownCtx); err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	if shutdownErr != nil {
		logger.Error("shutdown.error", "err", shutdownErr)
		os.Exit(1)
	}
	logger.Info("shutdown.done")
}

// indexBusFunc adapts a plain function to api.IndexBus. Keeps the wiring
// in main.go tight and avoids exporting a struct type for one method.
type indexBusFunc func(ctx context.Context, observationID, project, text string) error

// IndexObservation forwards to the wrapped function.
func (f indexBusFunc) IndexObservation(ctx context.Context, observationID, project, text string) error {
	return f(ctx, observationID, project, text)
}

// runHealthcheck performs a localhost probe of /health and returns a UNIX
// exit code. Container HEALTHCHECK invokes the binary with -healthcheck so
// the distroless image needs no extra shell.
func runHealthcheck(cfg *config.Config) int {
	url := fmt.Sprintf("http://127.0.0.1:%d/agentmemory/health", cfg.Port)
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		// Ignore body drain errors — status code is the signal.
		_ = err
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
