package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// ── PreToolUse ────────────────────────────────────────────────────────────────
// Fires before every tool call. Captures tool name + input, then defers the
// permission decision to Claude Code's built-in system.
//
// To block a tool: return SpecificOutput with PermissionDecision: "deny".
// To mutate the input: set UpdatedInput to the modified tool_input JSON.
func handlePreToolUse(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "pre_tool_use", base, cwd, map[string]any{
		"tool_name":  inp.ToolName,
		"tool_input": inp.ToolInput,
	})
	return SpecificOutput{HookSpecificOutput: HookSpecificPayload{
		HookEventName:      "PreToolUse",
		PermissionDecision: "defer",
	}}
}

// ── PostToolUse ───────────────────────────────────────────────────────────────
// Fires after a tool succeeds. Captures tool name, input, and output.
// For Edit/Write on *.md files it also triggers procedural memory seeding,
// mirroring the behaviour of agentmem-md-procedural-hook.
func handlePostToolUse(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "post_tool_use", base, cwd, map[string]any{
		"tool_name":   inp.ToolName,
		"tool_input":  inp.ToolInput,
		"tool_output": inp.ToolResponse,
	})
	if inp.ToolName == "Write" || inp.ToolName == "Edit" {
		var ti struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(inp.ToolInput, &ti) == nil && isMarkdown(ti.FilePath) {
			exe, _ := os.Executable()
			seedProceduralAsync(filepath.Dir(exe), ti.FilePath)
		}
	}
	return empty{}
}

// ── PostToolUseFailure ────────────────────────────────────────────────────────
func handlePostToolUseFailure(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "post_tool_use_failure", base, cwd, map[string]any{
		"tool_name":  inp.ToolName,
		"tool_input": inp.ToolInput,
		"error":      inp.Error,
	})
	return empty{}
}

// ── PostToolBatch ─────────────────────────────────────────────────────────────
// Fires after a batch of parallel tool calls completes.
// To block the next action: return BlockingOutput{Decision: "block", Reason: "..."}.
func handlePostToolBatch(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "post_tool_batch", base, cwd, map[string]any{})
	return empty{}
}

// ── PermissionRequest ─────────────────────────────────────────────────────────
// Fires when Claude Code is about to show a permission dialog.
// Default: pass through (show normal dialog). To auto-allow specific tools,
// return SpecificOutput with Decision: &PermissionDecisionBody{Behavior: "allow"}.
func handlePermissionRequest(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "permission_request", base, cwd, map[string]any{
		"tool_name":  inp.ToolName,
		"tool_input": inp.ToolInput,
	})
	return empty{}
}

// ── PermissionDenied ──────────────────────────────────────────────────────────
// Fires when the auto-mode classifier denies a tool call.
// Default: do not retry. To allow retry: return SpecificOutput with Retry: true.
func handlePermissionDenied(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ToolEventInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "permission_denied", base, cwd, map[string]any{
		"tool_name":  inp.ToolName,
		"tool_input": inp.ToolInput,
	})
	return empty{}
}

// ── Notification ──────────────────────────────────────────────────────────────
func handleNotification(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp NotificationInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "notification", base, cwd, map[string]any{
		"message": inp.Message,
		"title":   inp.Title,
	})
	return empty{}
}

// ── UserPromptSubmit ──────────────────────────────────────────────────────────
// Fires when the user submits a prompt. Increments the turn counter, then
// performs a synchronous recall-summary and injects relevant memory context.
func handleUserPromptSubmit(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp UserPromptInput
	json.Unmarshal(raw, &inp) //nolint:errcheck

	// Increment turn counter in trace state
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

// ── UserPromptExpansion ───────────────────────────────────────────────────────
// Fires when a slash command expands into a prompt. Same recall-and-inject
// logic as UserPromptSubmit.
func handleUserPromptExpansion(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp UserPromptInput
	json.Unmarshal(raw, &inp) //nolint:errcheck

	// Always capture the expansion text — independently of whether recall fires.
	text := inp.Expansion
	if text == "" {
		text = inp.Prompt
	}
	capture(cfg, "user_prompt", base, cwd, map[string]any{
		"user_prompt": text,
		"source":      "expansion",
	})

	query := text
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
		HookEventName:     "UserPromptExpansion",
		AdditionalContext: "[agent-mem] Relevant context from memory:\n" + summary,
	}}
}

// ── SessionStart ──────────────────────────────────────────────────────────────
// Fires when a session begins or resumes. Initialises (or preserves) per-session
// trace state, captures the source/model, then performs a source-aware sync
// recall and returns additionalContext + sessionTitle.
func handleSessionStart(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp SessionStartInput
	json.Unmarshal(raw, &inp) //nolint:errcheck

	// Reset trace state for new sessions; preserve offset for resume/compact.
	withTraceLock(base.SessionID, func(s *TraceState) {
		if inp.Source == "startup" || inp.Source == "clear" || inp.Source == "" {
			s.Turn = 0
			s.TranscriptOffset = 0
			s.InjectedIDs = nil
		}
		// resume / compact: keep existing turn and offset
	})

	capture(cfg, "session_start", base, cwd, map[string]any{
		"source": inp.Source,
		"model":  inp.Model,
	})

	// Source-aware recall query
	query := sessionStartQuery(inp.Source, cfg.Project)
	if significantTokenCount(query) < 2 {
		return empty{}
	}
	// Short timeout so a slow Medha doesn't stall the session startup
	summary, err := recallSummaryShort(cfg, query, cfg.Project)
	if err != nil || summary == "" {
		return empty{}
	}

	return SpecificOutput{HookSpecificOutput: HookSpecificPayload{
		HookEventName:     "SessionStart",
		AdditionalContext: sessionStartPrefix(inp.Source) + summary,
		SessionTitle:      sessionTitleFromGit(cwd),
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

func sessionTitleFromGit(cwd string) string {
	branch, _ := gitOut(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	root, err := gitOut(cwd, "rev-parse", "--show-toplevel")
	project := filepath.Base(cwd)
	if err == nil && root != "" {
		project = filepath.Base(root)
	}
	if branch != "" && branch != "HEAD" {
		return project + "/" + branch
	}
	return project
}

// ── Stop ──────────────────────────────────────────────────────────────────────
// Fires at the end of every assistant turn (NOT session end — see SessionEnd).
// Atomically pre-claims the new transcript bytes in trace state (prevents
// duplicate ingest across concurrent processes), ingests the delta, then posts
// a cheap incremental "stop" checkpoint. Full consolidation is deferred to
// SessionEnd so the pipeline runs at most once per session.
func handleStop(cfg config, cwd string, raw []byte, base BaseInput) any {
	var turn int
	var fromOffset, toOffset int64

	withTraceLock(base.SessionID, func(s *TraceState) {
		turn = s.Turn
		fromOffset = s.TranscriptOffset
		if base.TranscriptPath != "" {
			if info, err := os.Stat(base.TranscriptPath); err == nil {
				toOffset = info.Size()
				s.TranscriptOffset = toOffset // pre-claim range; prevents double-ingest
			}
		}
	})

	if base.TranscriptPath != "" && toOffset > fromOffset {
		ingestTranscriptRange(cfg, base, base.TranscriptPath, fromOffset, toOffset)
	}

	capture(cfg, "stop", base, cwd, map[string]any{"turn": turn})
	return empty{}
}

// ── StopFailure ───────────────────────────────────────────────────────────────
func handleStopFailure(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "stop_failure", base, cwd, map[string]any{})
	return empty{}
}

// ── SessionEnd ────────────────────────────────────────────────────────────────
// Fires when the session truly terminates. Ingests any remaining transcript
// delta, triggers the full consolidation pipeline (session_end hookType), then
// cleans up the trace state file for this session.
func handleSessionEnd(cfg config, cwd string, raw []byte, base BaseInput) any {
	var fromOffset, toOffset int64

	withTraceLock(base.SessionID, func(s *TraceState) {
		fromOffset = s.TranscriptOffset
		if base.TranscriptPath != "" {
			if info, err := os.Stat(base.TranscriptPath); err == nil {
				toOffset = info.Size()
				s.TranscriptOffset = toOffset
			}
		}
	})

	if base.TranscriptPath != "" && toOffset > fromOffset {
		ingestTranscriptRange(cfg, base, base.TranscriptPath, fromOffset, toOffset)
	}

	// session_end triggers the full consolidation pipeline in the server
	capture(cfg, "session_end", base, cwd, map[string]any{})

	deleteTraceState(base.SessionID)
	return empty{}
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
// To prevent a subagent from concluding: return BlockingOutput{Decision: "block"}.
func handleSubagentStop(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "subagent_stop", base, cwd, map[string]any{
		"agent_id":   base.AgentID,
		"agent_type": base.AgentType,
	})
	return empty{}
}

// ── PreCompact ────────────────────────────────────────────────────────────────
// Fires before context compaction. Flushes any un-ingested transcript delta
// as a safety snapshot (the compaction compresses the context window, not the
// transcript file, but snapshotting here gives an extra coverage checkpoint).
// To block compaction: return BlockingOutput{Decision: "block"}.
func handlePreCompact(cfg config, cwd string, raw []byte, base BaseInput) any {
	var fromOffset, toOffset int64

	withTraceLock(base.SessionID, func(s *TraceState) {
		fromOffset = s.TranscriptOffset
		if base.TranscriptPath != "" {
			if info, err := os.Stat(base.TranscriptPath); err == nil {
				toOffset = info.Size()
				s.TranscriptOffset = toOffset
			}
		}
	})

	if base.TranscriptPath != "" && toOffset > fromOffset {
		ingestTranscriptRange(cfg, base, base.TranscriptPath, fromOffset, toOffset)
	}

	capture(cfg, "pre_compact", base, cwd, map[string]any{})
	return empty{}
}

// ── PostCompact ───────────────────────────────────────────────────────────────
// Fires after compaction. Injects a summary of what was in the now-compacted
// context so the LLM does not lose track of recent work.
func handlePostCompact(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "post_compact", base, cwd, map[string]any{})

	query := "recent session context current task progress " + cfg.Project
	summary, err := recallSummary(cfg, query, cfg.Project)
	if err != nil || summary == "" {
		return empty{}
	}

	return SystemMessageOutput{
		SystemMessage: "[agent-mem] Context from compacted conversation:\n" + summary,
	}
}

// ── InstructionsLoaded ────────────────────────────────────────────────────────
func handleInstructionsLoaded(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp InstructionsLoadedInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "instructions_loaded", base, cwd, map[string]any{
		"path":   inp.Path,
		"reason": inp.Reason,
	})
	return empty{}
}

// ── ConfigChange ──────────────────────────────────────────────────────────────
// To block a config change: return BlockingOutput{Decision: "block"}.
func handleConfigChange(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ConfigChangeInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "config_change", base, cwd, map[string]any{
		"source": inp.Source,
	})
	return empty{}
}

// ── CwdChanged ────────────────────────────────────────────────────────────────
func handleCwdChanged(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp CwdChangedInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "cwd_changed", base, cwd, map[string]any{
		"new_cwd": inp.NewCWD,
	})
	return empty{}
}

// ── FileChanged ───────────────────────────────────────────────────────────────
func handleFileChanged(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp FileChangedInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "file_changed", base, cwd, map[string]any{
		"file_path": inp.FilePath,
	})
	return empty{}
}

// ── WorktreeCreate ────────────────────────────────────────────────────────────
// Fires when Claude Code is about to create a worktree.
// Returning an empty worktreePath lets the system choose a default path.
// To specify a custom path: set HookSpecificPayload.WorktreePath.
func handleWorktreeCreate(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp WorktreeInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "worktree_create", base, cwd, map[string]any{
		"branch": inp.Branch,
	})
	return empty{}
}

// ── WorktreeRemove ────────────────────────────────────────────────────────────
func handleWorktreeRemove(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp WorktreeInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "worktree_remove", base, cwd, map[string]any{
		"worktree_path": inp.WorktreePath,
	})
	return empty{}
}

// ── Elicitation ───────────────────────────────────────────────────────────────
// Fires when an MCP server requests user input. Default: pass through (show UI).
// To auto-respond: return SpecificOutput with Action ("accept"/"decline"/"cancel")
// and Content populated with the form values.
func handleElicitation(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ElicitationInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "elicitation", base, cwd, map[string]any{
		"server_name": inp.ServerName,
		"message":     inp.Message,
	})
	return empty{}
}

// ── ElicitationResult ─────────────────────────────────────────────────────────
func handleElicitationResult(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp ElicitationResultInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "elicitation_result", base, cwd, map[string]any{
		"server_name": inp.ServerName,
		"action":      inp.Action,
	})
	return empty{}
}

// ── TeammateIdle ──────────────────────────────────────────────────────────────
// Fires when a teammate agent is about to go idle.
// To keep the teammate alive: return BlockingOutput{Decision: "block"}.
func handleTeammateIdle(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "teammate_idle", base, cwd, map[string]any{
		"agent_id":   base.AgentID,
		"agent_type": base.AgentType,
	})
	return empty{}
}

// ── TaskCreated ───────────────────────────────────────────────────────────────
// Fires when a task is being created.
// To block creation: return BlockingOutput{Decision: "block"}.
func handleTaskCreated(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp TaskInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "task_created", base, cwd, map[string]any{
		"task_id":     inp.TaskID,
		"title":       inp.Title,
		"description": inp.Description,
	})
	return empty{}
}

// ── TaskCompleted ─────────────────────────────────────────────────────────────
// Fires when a task is marked as completed.
// To block completion: return BlockingOutput{Decision: "block"}.
func handleTaskCompleted(cfg config, cwd string, raw []byte, base BaseInput) any {
	var inp TaskInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "task_completed", base, cwd, map[string]any{
		"task_id": inp.TaskID,
		"title":   inp.Title,
	})
	return empty{}
}

// ── MessageDisplay ────────────────────────────────────────────────────────────
// Fires for every chunk of streamed assistant text — very high frequency.
// Rate-limited to one capture per session using a tmp lock file.
// Default: pass through (no display replacement).
// To replace the displayed text: set HookSpecificPayload.DisplayContent.
func handleMessageDisplay(cfg config, cwd string, raw []byte, base BaseInput) any {
	if base.SessionID == "" {
		return empty{}
	}
	lockFile := filepath.Join(os.TempDir(), "agentmem-msgdisplay-"+sanitizeID(base.SessionID))
	if fileExists(lockFile) {
		return empty{}
	}
	if err := os.WriteFile(lockFile, []byte{}, 0600); err != nil {
		return empty{}
	}

	var inp MessageDisplayInput
	json.Unmarshal(raw, &inp) //nolint:errcheck
	capture(cfg, "message_display", base, cwd, map[string]any{
		"content_preview": truncate(inp.Content, 500),
	})
	return empty{}
}

// ── Setup ─────────────────────────────────────────────────────────────────────
// Fires during --init / --maintenance runs.
func handleSetup(cfg config, cwd string, raw []byte, base BaseInput) any {
	capture(cfg, "setup", base, cwd, map[string]any{})
	return empty{}
}

// ── Unknown ───────────────────────────────────────────────────────────────────
// Forward-compatibility catch-all for hook types added after this binary.
func handleUnknown(cfg config, cwd string, raw []byte, base BaseInput) any {
	if base.HookEventName != "" {
		capture(cfg, "unknown", base, cwd, map[string]any{
			"event_name": base.HookEventName,
		})
	}
	return empty{}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func isMarkdown(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

// sanitizeID strips characters unsafe for use in file names.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// seedProceduralAsync spawns agentmem-seed-procedural in the background,
// searching for the binary next to the running executable then in PATH.
func seedProceduralAsync(dir, filePath string) {
	seedBin := filepath.Join(dir, "agentmem-seed-procedural")
	if _, err := os.Stat(seedBin); err != nil {
		if found, err := exec.LookPath("agentmem-seed-procedural"); err == nil {
			seedBin = found
		} else {
			return
		}
	}
	cmd := exec.Command(seedBin, filePath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
}
