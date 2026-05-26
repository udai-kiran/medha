// Package models holds the canonical Go structs shared across capture, search,
// and consolidation. JSON tags here are the source of truth for the
// /agentmemory/* API contracts — keep them aligned with the Python pydantic
// models in py/agent_mem/models.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// HookType enumerates the agent lifecycle events agent_mem ingests.
//
// New hook types should be appended; do not renumber existing constants
// because hookType is the canonical wire string (case-sensitive).
type HookType string

const (
	HookSessionStart    HookType = "session_start"
	HookSessionEnd      HookType = "session_end"
	HookUserPrompt      HookType = "user_prompt"
	HookPreToolUse      HookType = "pre_tool_use"
	HookPostToolUse     HookType = "post_tool_use"
	HookPostToolFailure HookType = "post_tool_failure"
	HookSubagentEnd     HookType = "subagent_end"
	HookNotification    HookType = "notification"
)

// validHookTypes lets us reject unknown enum values at parse time.
var validHookTypes = map[HookType]struct{}{
	HookSessionStart:    {},
	HookSessionEnd:      {},
	HookUserPrompt:      {},
	HookPreToolUse:      {},
	HookPostToolUse:     {},
	HookPostToolFailure: {},
	HookSubagentEnd:     {},
	HookNotification:    {},
}

// IsValid reports whether h is a known hook type.
func (h HookType) IsValid() bool {
	_, ok := validHookTypes[h]
	return ok
}

// MarshalJSON serialises as the bare string.
func (h HookType) MarshalJSON() ([]byte, error) {
	if !h.IsValid() {
		return nil, fmt.Errorf("models: invalid HookType %q", string(h))
	}
	return json.Marshal(string(h))
}

// UnmarshalJSON rejects unknown values.
func (h *HookType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	candidate := HookType(s)
	if !candidate.IsValid() {
		return fmt.Errorf("models: unknown HookType %q", s)
	}
	*h = candidate
	return nil
}

// HookPayload is the body the agent sends to POST /agentmemory/observe.
// Field names match the wire contract; do not change tags without updating
// the OpenAPI document (Task 2 seeded; Task 8 extends).
type HookPayload struct {
	HookType  HookType        `json:"hookType"`
	SessionID string          `json:"sessionId"`
	Project   string          `json:"project,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// Validate enforces presence + enum on the inbound payload. Returns the
// first error encountered — handlers should turn this into a 400 response.
func (p *HookPayload) Validate() error {
	if p == nil {
		return errors.New("HookPayload: nil")
	}
	if !p.HookType.IsValid() {
		return fmt.Errorf("HookPayload: invalid or missing hookType %q", string(p.HookType))
	}
	if p.SessionID == "" {
		return errors.New("HookPayload: sessionId required")
	}
	if p.Timestamp.IsZero() {
		return errors.New("HookPayload: timestamp required")
	}
	// Data may be empty for some hooks (notification, session_start with no extras).
	return nil
}
