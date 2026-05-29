package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/udai-kiran/medha/internal/search"
	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

func openMCPStore(t *testing.T) *state.Store {
	return testutil.OpenStore(t)
}

func newTestServer(t *testing.T) (*Server, *state.Store) {
	t.Helper()
	store := openMCPStore(t)
	bm25, _ := search.NewBM25(context.Background(), store)
	hybrid := &search.Hybrid{BM25: bm25, K: 60}
	srv := NewServer("agent_mem", "0.0.1", nil)
	RegisterMemoryTools(srv, MemoryToolsDeps{Store: store, Search: hybrid})
	RegisterMemoryResources(srv, MemoryToolsDeps{Store: store, Search: hybrid})
	RegisterMemoryPrompts(srv)
	return srv, store
}

// drive sends a request and returns the parsed response.
func drive(t *testing.T, srv *Server, req string) Response {
	t.Helper()
	var in bytes.Buffer
	in.WriteString(req)
	in.WriteString("\n")
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), &in, &out); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(&out).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; raw=%q", err, out.String())
	}
	return resp
}

func TestInitialize(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", res["protocolVersion"], ProtocolVersion)
	}
	caps := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Error("missing tools capability")
	}
}

func TestToolsList(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	tools := resp.Result.(map[string]any)["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	for _, want := range []string{"smart-search", "recall", "remember", "forget", "session-history", "status"} {
		if !sliceContains(names, want) {
			t.Errorf("missing tool %q in %v", want, names)
		}
	}
}

func TestRememberThenRecall(t *testing.T) {
	srv, _ := newTestServer(t)

	// remember
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remember","arguments":{"project":"p","type":"fact","title":"Use jose","content":"jose for JWT"}}}`
	resp := drive(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("remember error: %+v", resp.Error)
	}
	// The tool result is wrapped in MCP content.
	res := resp.Result.(map[string]any)
	contentArr := res["content"].([]any)
	inner := contentArr[0].(map[string]any)["text"].(string)
	var rememberOut struct {
		MemoryID string `json:"memoryId"`
	}
	_ = json.Unmarshal([]byte(inner), &rememberOut)
	if rememberOut.MemoryID == "" {
		t.Fatalf("no memoryId in remember response: %s", inner)
	}

	// recall
	recallBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"memoryId":"` + rememberOut.MemoryID + `"}}}`
	resp = drive(t, srv, recallBody)
	if resp.Error != nil {
		t.Fatalf("recall error: %+v", resp.Error)
	}
	recallText := resp.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(recallText, "Use jose") {
		t.Errorf("recall didn't return saved memory: %s", recallText)
	}
}

func TestStatusTool(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}`)
	if resp.Error != nil {
		t.Fatalf("status error: %+v", resp.Error)
	}
	text := resp.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "schemaVersion") {
		t.Errorf("missing schemaVersion in: %s", text)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"nonexistent/method"}`)
	if resp.Error == nil || resp.Error.Code != ErrMethodNotFound {
		t.Errorf("expected MethodNotFound, got %+v", resp.Error)
	}
}

func TestUnknownTool(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"no-such-tool","arguments":{}}}`)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "unknown tool") {
		t.Errorf("expected unknown tool error, got %+v", resp.Error)
	}
}

func TestResourcesListAndRead(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	resources := resp.Result.(map[string]any)["resources"].([]any)
	if len(resources) == 0 {
		t.Fatal("no resources listed")
	}

	resp = drive(t, srv, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"agentmemory://status"}}`)
	if resp.Error != nil {
		t.Fatalf("read: %+v", resp.Error)
	}
	contents := resp.Result.(map[string]any)["contents"].([]any)
	if len(contents) == 0 {
		t.Fatal("empty resource contents")
	}
}

func TestNotificationNoResponse(t *testing.T) {
	srv, _ := newTestServer(t)
	var in bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","method":"initialized"}` + "\n")
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), &in, &out); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("notification should not produce a response; got %q", out.String())
	}
}

func TestHTTPHandler(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.HTTPHandler()

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := newHTTPReq(t, "POST", "/", body)
	rec := newHTTPRec(t)
	h.ServeHTTP(rec, req)
	if rec.code != 200 {
		t.Fatalf("status = %d, body=%s", rec.code, rec.body.String())
	}
	var resp Response
	if err := json.NewDecoder(&rec.body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Errorf("err = %+v", resp.Error)
	}
}

func TestConcurrentReadsSafe(t *testing.T) {
	srv, _ := newTestServer(t)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = drive(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
			}
		}()
	}
	wg.Wait()
}

// Tiny HTTP test helpers — keep them here so the test file is self-contained.

type httpRecorder struct {
	code   int
	hdr    http.Header
	body   bytes.Buffer
}

func (r *httpRecorder) Header() http.Header {
	if r.hdr == nil {
		r.hdr = http.Header{}
	}
	return r.hdr
}

func (r *httpRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *httpRecorder) WriteHeader(code int)        { r.code = code }

func newHTTPRec(t *testing.T) *httpRecorder { t.Helper(); return &httpRecorder{code: 200} }

func newHTTPReq(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
