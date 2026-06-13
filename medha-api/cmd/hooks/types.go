package main

import "encoding/json"

// --- Input types ---

// BaseInput contains fields present in all hook events.
type BaseInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	HookEventName  string `json:"hook_event_name"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
}

// ToolEventInput extends BaseInput with tool-specific fields.
// Used by PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest, PermissionDenied.
type ToolEventInput struct {
	BaseInput
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"` // string or structured object
	Error        string          `json:"error,omitempty"`          // set on PostToolUseFailure
}

// UserPromptInput extends BaseInput with prompt / expansion text.
type UserPromptInput struct {
	BaseInput
	Prompt    string `json:"prompt,omitempty"`
	Expansion string `json:"expansion,omitempty"`
}

// NotificationInput extends BaseInput with notification message and title.
type NotificationInput struct {
	BaseInput
	Message string `json:"message"`
	Title   string `json:"title"`
}

// SessionStartInput extends BaseInput with session source and model.
type SessionStartInput struct {
	BaseInput
	Source string `json:"source"`
	Model  string `json:"model"`
}

// InstructionsLoadedInput extends BaseInput with load path and reason.
type InstructionsLoadedInput struct {
	BaseInput
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ConfigChangeInput extends BaseInput with the config source.
type ConfigChangeInput struct {
	BaseInput
	Source string `json:"source"`
}

// CwdChangedInput extends BaseInput with the new working directory.
type CwdChangedInput struct {
	BaseInput
	NewCWD string `json:"new_cwd"`
}

// FileChangedInput extends BaseInput with the changed file path.
type FileChangedInput struct {
	BaseInput
	FilePath string `json:"file_path"`
}

// ElicitationInput extends BaseInput with elicitation metadata.
type ElicitationInput struct {
	BaseInput
	ServerName string          `json:"server_name"`
	Message    string          `json:"message"`
	Schema     json.RawMessage `json:"schema"`
}

// ElicitationResultInput extends BaseInput with the user's elicitation response.
type ElicitationResultInput struct {
	BaseInput
	ServerName string          `json:"server_name"`
	Action     string          `json:"action"`
	Content    json.RawMessage `json:"content"`
}

// TaskInput extends BaseInput with task creation/completion fields.
type TaskInput struct {
	BaseInput
	TaskID      string `json:"task_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// MessageDisplayInput extends BaseInput with the assistant message content.
type MessageDisplayInput struct {
	BaseInput
	Content string `json:"content"`
}

// WorktreeInput extends BaseInput with worktree path and branch.
type WorktreeInput struct {
	BaseInput
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}

// --- Output types ---

// empty is the no-op pass-through output ({}).
type empty struct{}

// SpecificOutput wraps hookSpecificOutput for hooks that require a typed response.
type SpecificOutput struct {
	HookSpecificOutput HookSpecificPayload `json:"hookSpecificOutput"`
}

// HookSpecificPayload holds the per-event payload inside hookSpecificOutput.
// Fields are omitempty so unset values are not marshalled.
type HookSpecificPayload struct {
	HookEventName            string                  `json:"hookEventName"`
	AdditionalContext        string                  `json:"additionalContext,omitempty"`
	PermissionDecision       string                  `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string                  `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage         `json:"updatedInput,omitempty"`
	UpdatedToolOutput        string                  `json:"updatedToolOutput,omitempty"`
	DisplayContent           string                  `json:"displayContent,omitempty"`
	Action                   string                  `json:"action,omitempty"`
	Content                  json.RawMessage         `json:"content,omitempty"`
	Retry                    bool                    `json:"retry,omitempty"`
	WorktreePath             string                  `json:"worktreePath,omitempty"`
	SessionTitle             string                  `json:"sessionTitle,omitempty"`
	WatchPaths               []string                `json:"watchPaths,omitempty"`
	ReloadSkills             bool                    `json:"reloadSkills,omitempty"`
	InitialUserMessage       string                  `json:"initialUserMessage,omitempty"`
	Decision                 *PermissionDecisionBody `json:"decision,omitempty"`
}

// PermissionDecisionBody is used inside HookSpecificPayload.Decision for PermissionRequest.
type PermissionDecisionBody struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
}

// BlockingOutput is used by hooks that can optionally block the ongoing action.
// Set Decision to "block" and Reason to explain why.
type BlockingOutput struct {
	Decision   string `json:"decision,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Continue   *bool  `json:"continue,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
}
