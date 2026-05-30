package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/udai-kiran/medha/internal/mcp"
	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

func newTestServer(t *testing.T) (*sdkmcp.Server, *state.Store) {
	t.Helper()
	store := testutil.OpenStore(t)
	bm25, _ := search.NewBM25(context.Background(), store)
	hybrid := &search.Hybrid{BM25: bm25, K: 60}
	srv := mcp.NewMemoryServer("agent_mem", "0.0.1", mcp.MemoryToolsDeps{
		Store: store, Search: hybrid,
	})
	return srv, store
}

// connect creates an in-process client session connected to the server.
func connect(ctx context.Context, t *testing.T, srv *sdkmcp.Server) *sdkmcp.ClientSession {
	t.Helper()
	t1, t2 := sdkmcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatal("srv.Connect:", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal("client.Connect:", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestToolsList(t *testing.T) {
	ctx := context.Background()
	srv, _ := newTestServer(t)
	cs := connect(ctx, t, srv)

	var names []string
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	for _, want := range []string{"smart-search", "recall", "remember", "forget", "session-history", "status"} {
		if !sliceContains(names, want) {
			t.Errorf("missing tool %q; got %v", want, names)
		}
	}
}

func TestRememberThenRecall(t *testing.T) {
	ctx := context.Background()
	srv, _ := newTestServer(t)
	cs := connect(ctx, t, srv)

	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "remember",
		Arguments: map[string]any{"project": "p", "type": "fact", "title": "Use jose", "content": "jose for JWT"},
	})
	if err != nil {
		t.Fatal("remember:", err)
	}
	if res.IsError {
		t.Fatalf("remember IsError: %v", res.Content)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	var out struct {
		MemoryID string `json:"memoryId"`
	}
	_ = json.Unmarshal([]byte(text), &out)
	if out.MemoryID == "" {
		t.Fatalf("no memoryId in: %s", text)
	}

	res, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "recall",
		Arguments: map[string]any{"memoryId": out.MemoryID},
	})
	if err != nil {
		t.Fatal("recall:", err)
	}
	if res.IsError {
		t.Fatalf("recall IsError: %v", res.Content)
	}
	recallText := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(recallText, "Use jose") {
		t.Errorf("recall didn't return saved memory: %s", recallText)
	}
}

func TestStatusTool(t *testing.T) {
	ctx := context.Background()
	srv, _ := newTestServer(t)
	cs := connect(ctx, t, srv)

	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "status",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("status IsError: %v", res.Content)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "schemaVersion") {
		t.Errorf("missing schemaVersion in: %s", text)
	}
}

func TestUnknownTool(t *testing.T) {
	ctx := context.Background()
	srv, _ := newTestServer(t)
	cs := connect(ctx, t, srv)

	_, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "no-such-tool",
		Arguments: map[string]any{},
	})
	if err == nil {
		t.Error("expected error for unknown tool, got nil")
	}
}

func TestResourcesList(t *testing.T) {
	ctx := context.Background()
	srv, _ := newTestServer(t)
	cs := connect(ctx, t, srv)

	var uris []string
	for r, err := range cs.Resources(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		uris = append(uris, r.URI)
	}
	if !sliceContains(uris, "agentmemory://status") {
		t.Errorf("missing agentmemory://status; got %v", uris)
	}
}

func TestResourceRead(t *testing.T) {
	ctx := context.Background()
	srv, _ := newTestServer(t)
	cs := connect(ctx, t, srv)

	res, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "agentmemory://status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) == 0 {
		t.Fatal("empty resource contents")
	}
	if !strings.Contains(res.Contents[0].Text, "schemaVersion") {
		t.Errorf("missing schemaVersion in resource: %s", res.Contents[0].Text)
	}
}

func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
