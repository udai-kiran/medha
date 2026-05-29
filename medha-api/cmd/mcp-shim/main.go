// Command mcp-shim is a lightweight MCP server that proxies to the running
// agent_mem API when available, and falls back to 7 core tools via a direct
// PostgreSQL connection when the API is unreachable.
//
// Usage:
//
//	AGENTMEMORY_URL=http://localhost:3111 ./agent-mem-mcp-shim
//
// If AGENTMEMORY_URL is unreachable on first request, the shim switches to
// direct-DB mode using the same POSTGRES_* env vars as the main server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	agentmemURL := os.Getenv("AGENTMEMORY_URL")
	if agentmemURL == "" {
		agentmemURL = "http://localhost:3111"
	}

	// Probe the API.
	apiReachable := probeAPI(agentmemURL)

	if apiReachable {
		// Full proxy mode: forward all JSON-RPC calls to the HTTP MCP endpoint.
		logger.Info("mcp.shim.proxy", "url", agentmemURL)
		runProxy(agentmemURL)
		return
	}

	// Fallback mode: direct DB connection with core tools only.
	logger.Info("mcp.shim.fallback", "reason", "API unreachable")

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
		logger.Error("shim.state.open", "err", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	bm25, err := search.NewBM25(rootCtx, store)
	if err != nil {
		logger.Error("shim.search.bm25", "err", err)
		os.Exit(1)
	}
	vec, err := search.NewVectorIndex(rootCtx, store, &search.PythonEmbedder{BaseURL: cfg.PythonServiceURL})
	if err != nil {
		logger.Error("shim.search.vector", "err", err)
		os.Exit(1)
	}
	graph := search.NewGraphIndex(store)
	hybrid := &search.Hybrid{BM25: bm25, Vector: vec, Graph: graph, K: 60}

	deps := mcp.MemoryToolsDeps{Store: store, Search: hybrid, PythonBaseURL: cfg.PythonServiceURL}
	srv := mcp.NewServer("agent_mem_shim", "0.1.0", logger)
	mcp.RegisterMemoryTools(srv, deps)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	logger.Info("mcp.shim.serve.start")
	if err := srv.Serve(rootCtx, os.Stdin, os.Stdout); err != nil {
		logger.Error("mcp.shim.serve.failed", "err", err)
		os.Exit(1)
	}
}

func probeAPI(baseURL string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(baseURL + "/agentmemory/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// runProxy reads JSON-RPC from stdin and forwards each request to the API's
// HTTP MCP endpoint, writing responses to stdout.
func runProxy(baseURL string) {
	endpoint := baseURL + "/agentmemory/mcp"
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	client := &http.Client{Timeout: 60 * time.Second}

	for {
		var req json.RawMessage
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			continue
		}
		data, _ := json.Marshal(req)
		resp, err := client.Post(endpoint, "application/json", bytes.NewReader(data))
		if err != nil {
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": nil,
				"error": map[string]any{"code": -32603, "message": fmt.Sprintf("proxy error: %v", err)},
			})
			continue
		}
		var result json.RawMessage
		_ = json.NewDecoder(resp.Body).Decode(&result)
		_ = resp.Body.Close()
		_ = enc.Encode(result)
	}
}
