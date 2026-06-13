package main

import (
	"encoding/json"
	"time"
)

// capture posts an observation asynchronously (fire-and-forget via asyncWG).
func capture(cfg config, hookType string, base BaseInput, cwd string, data any) {
	observeAsync(cfg, ObservationPayload{
		HookType:  hookType,
		SessionID: base.SessionID,
		Project:   cfg.Project,
		CWD:       cwd,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	})
}

// ── SessionStart ──────────────────────────────────────────────────────────────
// Fires when a Codex session begins or resumes. Initialises per-session trace
// state, captures source/model, then performs a source-aware recall and injects
// additionalContext.
//
// Note: Codex SessionStart output does not support sessionTitle (unlike Claude Code).
func handleSessionStart(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp SessionStartInput
	json.Unmarshal(raw, &inp) //nolint:errcheck

	withTraceLock(base.SessionID, func(s *TraceState) {
		if inp.Source == "startup" || inp.Source == "clear" || inp.Source == "" {
			s.Turn = 0
		}
		// resume / compact: keep existing turn counter
	})

	capture(cfg, "session_start", base, cwd, map[string]any{
		"source": inp.Source,
		"model":  inp.Model,
	})

	query := sessionStartQuery(inp.Source, cfg.Project)
	if significantTokenCount(query) < 2 {
		return empty{}
	}
	summary, err := recallSummaryShort(cfg, query, cfg.Project)
	if err != nil || summary == "" {
		return empty{}
	}

	return SpecificOutput{HookSpecificOutput: HookSpecificPayload{
		HookEventName:     "SessionStart",
		AdditionalContext: sessionStartPrefix(inp.Source) + summary,
	}}
}

func sessionStartQuery(source, project string) string {
	switch source {
	case "startup":
		return "project overview recent decisions open tasks " + project
	case "resume":
		return "where left off last session recent work incomplete tasks " + project
	case "clear":
		return "project context key decisions " + project
	case "compact":
		return "recent session context compacted conversation " + project
	default:
		return "project context " + project
	}
}

func sessionStartPrefix(source string) string {
	switch source {
	case "startup":
		return "[agent-mem] Project briefing:\n"
	case "resume":
		return "[agent-mem] Resuming — where you left off:\n"
	case "clear":
		return "[agent-mem] Project context (history cleared):\n"
	case "compact":
		return "[agent-mem] Context from compacted conversation:\n"
	default:
		return "[agent-mem] Relevant memory:\n"
	}
}

// ── UserPromptSubmit ──────────────────────────────────────────────────────────
// Fires when the user submits a prompt. Increments the turn counter, then
// performs a synchronous recall-summary and injects relevant memory context.
func handleUserPromptSubmit(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp UserPromptInput
	json.Unmarshal(raw, &inp) //nolint:errcheck

	withTraceLock(base.SessionID, func(s *TraceState) {
		s.Turn++
	})

	// Always capture the prompt — independently of whether recall fires.
	capture(cfg, "user_prompt", base, cwd, map[string]any{
		"user_prompt": inp.Prompt,
	})

	query := inp.Prompt
	if len([]rune(query)) > 300 {
		query = string([]rune(query)[:300])
	}
	if significantTokenCount(query) < 2 {
		return empty{}
	}

	summary, err := recallSummary(cfg, query, cfg.Project)
	if err != nil || summary == "" {
		return empty{}
	}

	return SpecificOutput{HookSpecificOutput: HookSpecificPayload{
		HookEventName:     "UserPromptSubmit",
		AdditionalContext: "[agent-mem] Relevant context from memory:\n" + summary,
	}}
}

// ── PreToolUse ────────────────────────────────────────────────────────────────
// Fires before every tool call. Captures tool name + input, then defers the
// permission decision to Codex's built-in system.
func handlePreToolUse(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "pre_tool_use", base, cwd, map[string]any{
		"tool_name":  inp.ToolName,
		"tool_input": inp.ToolInput,
	})
	return empty{}
}

// ── PostToolUse ───────────────────────────────────────────────────────────────
// Fires after a tool succeeds. Captures tool name, input, and response.
func handlePostToolUse(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "post_tool_use", base, cwd, map[string]any{
		"tool_name":     inp.ToolName,
		"tool_input":    inp.ToolInput,
		"tool_response": inp.ToolResponse,
	})
	return empty{}
}

// ── PermissionRequest ─────────────────────────────────────────────────────────
// Fires when Codex is about to show a permission dialog.
// Default: pass through (show normal dialog).
func handlePermissionRequest(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "permission_request", base, cwd, map[string]any{
		"tool_name":  inp.ToolName,
		"tool_input": inp.ToolInput,
	})
	return empty{}
}

// ── PreCompact ────────────────────────────────────────────────────────────────
// Fires before context compaction. Captures the event for the activity trace.
func handlePreCompact(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "pre_compact", base, cwd, map[string]any{})
	return empty{}
}

// ── PostCompact ───────────────────────────────────────────────────────────────
// Fires after compaction. Codex PostCompact does NOT support hookSpecificOutput,
// so memory context is surfaced via systemMessage instead of additionalContext.
func handlePostCompact(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp PostCompactInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "post_compact", base, cwd, map[string]any{
		"trigger": inp.Trigger,
	})

	query := "recent session context current task progress " + cfg.Project
	summary, err := recallSummary(cfg, query, cfg.Project)
	if err != nil || summary == "" {
		return empty{}
	}

	return SystemMessageOutput{
		SystemMessage: "[agent-mem] Context from compacted conversation:\n" + summary,
	}
}

// ── SubagentStart ─────────────────────────────────────────────────────────────
func handleSubagentStart(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "subagent_start", base, cwd, map[string]any{
		"agent_id":   base.AgentID,
		"agent_type": base.AgentType,
	})
	return empty{}
}

// ── SubagentStop ──────────────────────────────────────────────────────────────
func handleSubagentStop(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "subagent_stop", base, cwd, map[string]any{
		"agent_id":   base.AgentID,
		"agent_type": base.AgentType,
	})
	return empty{}
}

// ── Stop ──────────────────────────────────────────────────────────────────────
// Fires at the end of every assistant turn. Codex provides last_assistant_message
// directly in the event (no transcript parsing needed). Captures the turn summary,
// then — because Codex has no SessionEnd event — also fires a session_end
// observation to trigger the server-side consolidation pipeline.
//
// stop_hook_active is true when Stop fired as a consequence of a previous Stop
// hook returning decision:"block" (re-entrant guard). We skip the session_end
// trigger in that case to avoid kicking off consolidation mid-chain.
func handleStop(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp StopInput
	json.Unmarshal(raw, &inp) //nolint:errcheck

	var turn int
	withTraceLock(base.SessionID, func(s *TraceState) {
		turn = s.Turn
	})

	data := map[string]any{"turn": turn}
	if msg := inp.LastAssistantMessage; msg != "" {
		data["last_assistant_message"] = truncate(msg, 2000)
	}
	capture(cfg, "stop", base, cwd, data)

	// Codex has no SessionEnd hook. Trigger consolidation from Stop instead,
	// skipping only when stop_hook_active (re-entrant call from a block decision).
	if !inp.StopHookActive {
		capture(cfg, "session_end", base, cwd, map[string]any{})
	}

	return empty{}
}

