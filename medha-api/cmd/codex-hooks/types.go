package main

import "encoding/json"

// --- Input types ---

// BaseInput contains fields common to all Codex hook events.
// Source: codex-rs/hooks/schema/generated/*.command.input.schema.json
type BaseInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	HookEventName  string `json:"hook_event_name"`
	Model          string `json:"model"`
	TurnID         string `json:"turn_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
}

// ToolEventInput is used by PreToolUse, PostToolUse, and PermissionRequest.
type ToolEventInput struct {
	BaseInput
	ToolName     string          `json:"tool_name"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
}

// UserPromptInput is used by UserPromptSubmit.
type UserPromptInput struct {
	BaseInput
	Prompt string `json:"prompt"`
}

// SessionStartInput is used by SessionStart.
type SessionStartInput struct {
	BaseInput
	Source string `json:"source"` // startup | resume | clear | compact
}

// PostCompactInput is used by PostCompact.
type PostCompactInput struct {
	BaseInput
	Trigger string `json:"trigger"` // manual | auto
}

// StopInput is used by Stop. Codex provides last_assistant_message directly in
// the event, eliminating the need to parse the transcript file.
type StopInput struct {
	BaseInput
	LastAssistantMessage string `json:"last_assistant_message"`
	StopHookActive       bool   `json:"stop_hook_active"`
}

// --- Output types ---

// empty is the no-op pass-through output ({}).
type empty struct{}

// SpecificOutput wraps hookSpecificOutput for SessionStart, UserPromptSubmit,
// PreToolUse, and PostToolUse.
type SpecificOutput struct {
	HookSpecificOutput HookSpecificPayload `json:"hookSpecificOutput"`
}

// HookSpecificPayload holds the per-event payload inside hookSpecificOutput.
// Fields are omitempty so unset values are not marshalled.
type HookSpecificPayload struct {
	HookEventName            string          `json:"hookEventName"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
	PermissionDecision       string          `json:"permissionDecision,omitempty"` // allow|deny|ask; PreToolUse only
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
}

// PermissionRequestOutput is for PermissionRequest — uses nested decision.behavior,
// which differs from PreToolUse's flat permissionDecision field.
type PermissionRequestOutput struct {
	HookSpecificOutput PermissionRequestPayload `json:"hookSpecificOutput"`
}

// PermissionRequestPayload holds the PermissionRequest hookSpecificOutput.
type PermissionRequestPayload struct {
	HookEventName string                  `json:"hookEventName"`
	Decision      *PermissionDecisionBody `json:"decision,omitempty"`
}

// PermissionDecisionBody holds the allow/deny behavior for PermissionRequest.
type PermissionDecisionBody struct {
	Behavior string `json:"behavior"` // allow | deny
}

// SystemMessageOutput is returned by PostCompact. PostCompact does not support
// hookSpecificOutput in Codex; systemMessage surfaces context in the UI instead.
type SystemMessageOutput struct {
	SystemMessage string `json:"systemMessage"`
}
