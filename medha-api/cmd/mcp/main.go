// Command mcp is the agent_mem MCP server using Streamable HTTP transport.
// Listens on MCP_PORT (default 3114) and serves the full agent_mem tool surface.
// Claude Code config: claude mcp add agent-mem --transport http http://localhost:3114/mcp
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/mcp"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/telemetry"
)

func main() {
	cfg := config.FromEnv()
	logger := telemetry.NewLogger(cfg.LogLevel)
	if err := cfg.Validate(); err != nil {
		logger.Error("config.invalid", "err", err)
		os.Exit(2)
	}

	port := os.Getenv("MCP_PORT")
	if port == "" {
		port = "3114"
	}

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	bm25, err := search.NewBM25(rootCtx, store)
	if err != nil {
		logger.Error("search.bm25", "err", err)
		os.Exit(1)
	}
	vec, err := search.NewVectorIndex(rootCtx, store, &search.PythonEmbedder{
		BaseURL: cfg.PythonServiceURL,
	})
	if err != nil {
		logger.Error("search.vector", "err", err)
		os.Exit(1)
	}
	graph := search.NewGraphIndex(store)
	hybrid := &search.Hybrid{BM25: bm25, Vector: vec, Graph: graph, K: 60}

	srv := mcp.NewMemoryServer("agent_mem", "0.1.0", mcp.MemoryToolsDeps{
		Store:         store,
		Search:        hybrid,
		PythonBaseURL: cfg.PythonServiceURL,
	})

	handler := sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server {
		return srv
	}, &sdkmcp.StreamableHTTPOptions{Stateless: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("mcp.http.listen", "addr", fmt.Sprintf(":%s", port))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("mcp.http.failed", "err", err)
			os.Exit(1)
		}
	}()

	<-stop
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	logger.Info("mcp.http.shutdown")
}
