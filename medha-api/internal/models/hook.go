// Package models holds the canonical Go structs shared across capture, search,
// and consolidation. JSON tags here are the source of truth for the
// /agentmemory/* API contracts — keep them aligned with the Python pydantic
// models in medha-extraction/medha/models.
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
	// Original 8 hook types (kept for backward compatibility).
	HookSessionStart    HookType = "session_start"
	HookSessionEnd      HookType = "session_end"
	HookUserPrompt      HookType = "user_prompt"
	HookPreToolUse      HookType = "pre_tool_use"
	HookPostToolUse     HookType = "post_tool_use"
	HookPostToolFailure HookType = "post_tool_failure" // legacy alias
	HookSubagentEnd     HookType = "subagent_end"      // legacy alias
	HookNotification    HookType = "notification"

	// Extended hook types covering all 30 Claude Code hook events.
	HookPostToolUseFailure HookType = "post_tool_use_failure"
	HookPostToolBatch      HookType = "post_tool_batch"
	HookPermissionRequest  HookType = "permission_request"
	HookPermissionDenied   HookType = "permission_denied"
	HookSubagentStart      HookType = "subagent_start"
	HookSubagentStop       HookType = "subagent_stop"
	HookStopFailure        HookType = "stop_failure"
	HookSessionEndEvent    HookType = "session_end_event"
	HookPreCompact         HookType = "pre_compact"
	HookPostCompact        HookType = "post_compact"
	HookInstructionsLoaded HookType = "instructions_loaded"
	HookConfigChange       HookType = "config_change"
	HookCwdChanged         HookType = "cwd_changed"
	HookFileChanged        HookType = "file_changed"
	HookWorktreeCreate     HookType = "worktree_create"
	HookWorktreeRemove     HookType = "worktree_remove"
	HookElicitation        HookType = "elicitation"
	HookElicitationResult  HookType = "elicitation_result"
	HookTeammateIdle       HookType = "teammate_idle"
	HookTaskCreated        HookType = "task_created"
	HookTaskCompleted      HookType = "task_completed"
	HookMessageDisplay     HookType = "message_display"
	HookSetup              HookType = "setup"
	HookUnknown            HookType = "unknown"

	// Synthetic hook types used internally by the dispatcher.
	HookStop            HookType = "stop"            // per-turn incremental checkpoint
	HookTranscriptDelta HookType = "transcript_delta" // assistant text from transcript ingest
)

// validHookTypes lets us reject unknown enum values at parse time.
var validHookTypes = map[HookType]struct{}{
	HookSessionStart:       {},
	HookSessionEnd:         {},
	HookUserPrompt:         {},
	HookPreToolUse:         {},
	HookPostToolUse:        {},
	HookPostToolFailure:    {},
	HookSubagentEnd:        {},
	HookNotification:       {},
	HookPostToolUseFailure: {},
	HookPostToolBatch:      {},
	HookPermissionRequest:  {},
	HookPermissionDenied:   {},
	HookSubagentStart:      {},
	HookSubagentStop:       {},
	HookStopFailure:        {},
	HookSessionEndEvent:    {},
	HookPreCompact:         {},
	HookPostCompact:        {},
	HookInstructionsLoaded: {},
	HookConfigChange:       {},
	HookCwdChanged:         {},
	HookFileChanged:        {},
	HookWorktreeCreate:     {},
	HookWorktreeRemove:     {},
	HookElicitation:        {},
	HookElicitationResult:  {},
	HookTeammateIdle:       {},
	HookTaskCreated:        {},
	HookTaskCompleted:      {},
	HookMessageDisplay:     {},
	HookSetup:              {},
	HookUnknown:            {},
	HookStop:               {},
	HookTranscriptDelta:    {},
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
