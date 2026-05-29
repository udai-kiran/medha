// Command mcp is the agent_mem MCP stdio server.
// Reads JSON-RPC 2.0 requests on stdin, writes responses on stdout.
// Designed to be launched by an agent host (Claude Code, Cursor, Cline, …)
// rather than run interactively.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	deps := mcp.MemoryToolsDeps{Store: store, Search: hybrid, PythonBaseURL: cfg.PythonServiceURL}
	srv := mcp.NewServer("agent_mem", "0.1.0", logger)
	mcp.RegisterMemoryTools(srv, deps)
	mcp.RegisterMemoryResources(srv, deps)
	mcp.RegisterMemoryPrompts(srv)

	// Soft-shutdown handler.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		// Give Serve a moment to return after stdin closes.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	logger.Info("mcp.serve.start")
	if err := srv.Serve(rootCtx, os.Stdin, os.Stdout); err != nil {
		logger.Error("mcp.serve.failed", "err", err)
		os.Exit(1)
	}
	logger.Info("mcp.serve.done")
}
