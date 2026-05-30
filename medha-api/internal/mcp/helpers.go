package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Error is the JSON-RPC / MCP error shape used by all tool handlers.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp[%d]: %s", e.Code, e.Message)
}

const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

// MustParseArgs round-trips args through JSON into dst.
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

// ErrToRPC converts a Go error into an MCP Error.
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
