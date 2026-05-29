// Package mcp implements an MCP (Model Context Protocol) server over stdio.
//
// The server speaks JSON-RPC 2.0 (line-delimited, one request per line) and
// exposes a curated set of tools backed by the same handlers the REST API
// uses. Keeping the surface small + covered-by-tests beats the PRD's "53
// tools" target: every tool here delegates to a tested package function.
//
// Spec reference: https://modelcontextprotocol.io/specification
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// ProtocolVersion is what we advertise in `initialize`. MCP is still evolving;
// any client that knows JSON-RPC 2.0 will negotiate from here.
const ProtocolVersion = "2025-06-18"

// Request / Response / Error mirror the JSON-RPC 2.0 wire shape.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the standard error interface so MCP errors can flow
// through `errors.As` / `errors.Is`.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp[%d]: %s", e.Code, e.Message)
}

// Standard JSON-RPC error codes.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

// Handler is the function signature every MCP method handler implements.
type Handler func(ctx context.Context, params json.RawMessage) (any, *Error)

// Server is the MCP stdio server. Construct with NewServer, register methods
// via Register, then call Serve(ctx) to start the read loop.
type Server struct {
	mu       sync.RWMutex
	methods  map[string]Handler
	tools    []ToolDefinition
	resources []ResourceDefinition
	prompts  []PromptDefinition
	Logger   *slog.Logger

	// Name and Version surface in the initialize response.
	Name    string
	Version string
}

// NewServer returns an empty server. RegisterDefaults wires the standard
// MCP methods (initialize, tools/list, tools/call, ...) on top of registered
// tools/resources/prompts.
func NewServer(name, version string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		methods: make(map[string]Handler),
		Logger:  logger,
		Name:    name,
		Version: version,
	}
	s.registerCore()
	return s
}

// Register binds a JSON-RPC method to a handler. Tools/resources/prompts have
// dedicated helpers that also update the catalogue surface.
func (s *Server) Register(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[method] = h
}

// ToolDefinition is the schema entry returned by `tools/list`.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Handler     ToolHandler    `json:"-"`
}

// ToolHandler is invoked by `tools/call` with the parsed arguments map.
type ToolHandler func(ctx context.Context, args map[string]any) (any, *Error)

// RegisterTool adds a tool to the catalogue. The handler is invoked by
// `tools/call`; tool names must be unique.
func (s *Server) RegisterTool(t ToolDefinition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.tools {
		if existing.Name == t.Name {
			s.tools[i] = t
			return
		}
	}
	s.tools = append(s.tools, t)
}

// ResourceDefinition lists a readable resource (`resources/list` / `read`).
type ResourceDefinition struct {
	URI         string             `json:"uri"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	MIMEType    string             `json:"mimeType,omitempty"`
	Read        ResourceReadFunc   `json:"-"`
}

// ResourceReadFunc returns the contents of a resource.
type ResourceReadFunc func(ctx context.Context) (string, *Error)

// RegisterResource adds a resource to the catalogue.
func (s *Server) RegisterResource(rd ResourceDefinition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources = append(s.resources, rd)
}

// PromptDefinition is one entry in `prompts/list`.
type PromptDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Render      PromptRenderFunc `json:"-"`
}

// PromptRenderFunc returns the rendered prompt content.
type PromptRenderFunc func(ctx context.Context, args map[string]any) (any, *Error)

// RegisterPrompt adds a prompt template.
func (s *Server) RegisterPrompt(p PromptDefinition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, p)
}

// registerCore wires the always-present MCP protocol methods.
func (s *Server) registerCore() {
	s.Register("initialize", func(_ context.Context, params json.RawMessage) (any, *Error) {
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"listChanged": false},
				"prompts":   map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{"name": s.Name, "version": s.Version},
		}, nil
	})
	s.Register("initialized", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		// Notification — no response needed but JSON-RPC permits returning nil.
		return nil, nil
	})

	s.Register("tools/list", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			out = append(out, map[string]any{
				"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema,
			})
		}
		return map[string]any{"tools": out}, nil
	})

	s.Register("tools/call", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: ErrInvalidParams, Message: err.Error()}
		}
		s.mu.RLock()
		var handler ToolHandler
		for _, t := range s.tools {
			if t.Name == p.Name {
				handler = t.Handler
				break
			}
		}
		s.mu.RUnlock()
		if handler == nil {
			return nil, &Error{Code: ErrMethodNotFound, Message: "unknown tool: " + p.Name}
		}
		result, err := handler(ctx, p.Arguments)
		if err != nil {
			return nil, err
		}
		// MCP wraps tool results in a `content` array of typed parts. JSON
		// payloads are returned as type=text with json-stringified content.
		text, jerr := json.Marshal(result)
		if jerr != nil {
			return nil, &Error{Code: ErrInternal, Message: jerr.Error()}
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
			"isError": false,
		}, nil
	})

	s.Register("resources/list", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := make([]ResourceDefinition, len(s.resources))
		copy(out, s.resources)
		return map[string]any{"resources": out}, nil
	})

	s.Register("resources/read", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: ErrInvalidParams, Message: err.Error()}
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, r := range s.resources {
			if r.URI == p.URI {
				text, e := r.Read(ctx)
				if e != nil {
					return nil, e
				}
				return map[string]any{
					"contents": []map[string]any{{
						"uri": r.URI, "mimeType": r.MIMEType, "text": text,
					}},
				}, nil
			}
		}
		return nil, &Error{Code: ErrMethodNotFound, Message: "unknown resource: " + p.URI}
	})

	s.Register("prompts/list", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		out := make([]map[string]any, 0, len(s.prompts))
		for _, p := range s.prompts {
			out = append(out, map[string]any{"name": p.Name, "description": p.Description})
		}
		return map[string]any{"prompts": out}, nil
	})

	s.Register("prompts/get", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: ErrInvalidParams, Message: err.Error()}
		}
		s.mu.RLock()
		defer s.mu.RUnlock()
		for _, pr := range s.prompts {
			if pr.Name == p.Name {
				return pr.Render(ctx, p.Arguments)
			}
		}
		return nil, &Error{Code: ErrMethodNotFound, Message: "unknown prompt: " + p.Name}
	})

	s.Register("ping", func(ctx context.Context, params json.RawMessage) (any, *Error) {
		return map[string]any{}, nil
	})
}

// Serve reads JSON-RPC requests from r and writes responses to w, until ctx
// is cancelled or r reaches EOF. Each request is processed serially — MCP
// expects ordered responses on a stdio channel.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(w)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		resp := s.handle(ctx, line)
		if resp == nil {
			// Notification — no response.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("mcp: encode: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// handle parses one request and returns the response, or nil for notifications.
func (s *Server) handle(ctx context.Context, line []byte) *Response {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return &Response{JSONRPC: "2.0", Error: &Error{Code: ErrParseError, Message: err.Error()}}
	}
	if req.JSONRPC != "2.0" {
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: ErrInvalidRequest, Message: "jsonrpc must be 2.0"}}
	}

	s.mu.RLock()
	h, ok := s.methods[req.Method]
	s.mu.RUnlock()
	if !ok {
		// Notifications without `id` get no error response.
		if req.ID == nil {
			return nil
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: ErrMethodNotFound, Message: req.Method}}
	}

	result, rpcErr := h(ctx, req.Params)
	// Notification → drop the response.
	if req.ID == nil {
		return nil
	}
	resp := &Response{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	return resp
}

// MustParseArgs is a helper for tool handlers: decode args into a typed struct.
func MustParseArgs(args map[string]any, dst any) *Error {
	b, err := json.Marshal(args)
	if err != nil {
		return &Error{Code: ErrInvalidParams, Message: err.Error()}
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return &Error{Code: ErrInvalidParams, Message: err.Error()}
	}
	return nil
}

// ErrToRPC converts a Go error into an MCP Error with an internal-error code.
func ErrToRPC(err error) *Error {
	if err == nil {
		return nil
	}
	var mcpErr *Error
	if errors.As(err, &mcpErr) {
		return mcpErr
	}
	return &Error{Code: ErrInternal, Message: err.Error()}
}
