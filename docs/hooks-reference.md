# Claude Code Hooks Reference

Source: https://code.claude.com/docs/en/hooks  
Extracted: 2026-06-08

## What Are Hooks

Hooks are shell commands, HTTP endpoints, MCP tool calls, LLM prompts, or agent invocations that fire automatically at specific points in Claude Code's lifecycle. They can inject context, block actions, observe tool use, or trigger external systems.

---

## Hook Configuration Format

```json
{
  "hooks": {
    "EventName": [
      {
        "matcher": "ToolName or pattern",
        "hooks": [
          {
            "type": "command|http|mcp_tool|prompt|agent",
            "command": "path/to/script.sh",
            "timeout": 30,
            "async": false
          }
        ]
      }
    ]
  }
}
```

### Config file locations (highest to lowest precedence)

| File | Scope |
|---|---|
| Managed policy settings | Org-wide, cannot be overridden |
| `~/.claude/settings.json` | All projects |
| `.claude/settings.json` | Project (checked in) |
| `.claude/settings.local.json` | Project (not checked in) |
| Skill/agent frontmatter | Component lifetime |

---

## All Hook Events

### Lifecycle cadence

| Cadence | Events |
|---|---|
| Once per session | `SessionStart`, `SessionEnd`, `Setup` |
| Once per turn | `UserPromptSubmit`, `UserPromptExpansion`, `Stop`, `StopFailure` |
| Every tool call | `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `PermissionRequest`, `PermissionDenied` |
| Async / reactive | `FileChanged`, `CwdChanged`, `ConfigChange`, `WorktreeCreate`, `WorktreeRemove`, `Notification`, `InstructionsLoaded`, `PreCompact`, `PostCompact`, `SubagentStart`, `SubagentStop`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `Elicitation`, `ElicitationResult`, `MessageDisplay` |

---

## Input Schema (all events)

Every hook receives JSON on stdin (command) or as POST body (HTTP):

```json
{
  "session_id": "abc123",
  "transcript_path": "/path/to/transcript.jsonl",
  "cwd": "/current/working/directory",
  "permission_mode": "default|plan|acceptEdits|auto|dontAsk|bypassPermissions",
  "hook_event_name": "EventName",
  "effort": { "level": "low|medium|high|xhigh|max" },
  "agent_id": "subagent_id",
  "agent_type": "agent_name"
}
```

Plus event-specific fields (see per-event sections below).

---

## Output Format (command hooks)

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success — stdout parsed as JSON |
| `2` | Blocking error — execution stopped, stderr shown to Claude |
| Other | Non-blocking error — execution continues, stderr in debug log |

### JSON stdout structure (exit 0)

```json
{
  "continue": true,
  "stopReason": "shown if continue=false",
  "suppressOutput": false,
  "systemMessage": "warning shown to user",
  "hookSpecificOutput": {
    "hookEventName": "EventName",
    "additionalContext": "context injected into Claude's prompt",
    "decision": "block",
    "permissionDecision": "allow|deny|ask|defer"
  }
}
```

> **Note:** Hook stdout is capped at 10,000 characters. Excess is saved to a file with a preview.

---

## Event Reference

### UserPromptSubmit

Fires before Claude processes each user message. The primary hook for injecting memory context.

**Supports blocking:** Yes  
**Default timeout:** 30 seconds

**Input (additional fields):**
```json
{
  "prompt": "the user's message text",
  "permission_mode": "default"
}
```

**Output — inject context:**
```json
{
  "hookSpecificOutput": {
    "hookEventName": "UserPromptSubmit",
    "additionalContext": "text injected before Claude sees the prompt"
  }
}
```

**Output — block prompt:**
```json
{
  "decision": "block",
  "reason": "reason shown to user"
}
```

---

### UserPromptExpansion

Fires when a slash command (e.g. `/foo`) expands into a full prompt.

**Supports blocking:** Yes  
**Matchers:** command name

**Input (additional fields):**
```json
{
  "command": "foo",
  "expansion": "the expanded prompt text"
}
```

**Output — block:**
```json
{
  "decision": "block",
  "reason": "reason text"
}
```

---

### PreToolUse

Fires before each tool call. Can allow, deny, or modify the tool input.

**Supports blocking:** Yes  
**Matchers:** tool name (e.g. `Bash`, `Edit|Write`, `mcp__.*`)

**Input (additional fields):**
```json
{
  "tool_name": "Bash",
  "tool_input": { "command": "npm test" },
  "permission_mode": "default"
}
```

**Output — control permission:**
```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow|deny|ask|defer",
    "permissionDecisionReason": "reason",
    "updatedToolInput": { "modified": "input" },
    "additionalContext": "context for Claude"
  }
}
```

---

### PostToolUse

Fires after each successful tool call. Can block subsequent execution.

**Supports blocking:** Yes  
**Matchers:** tool name

**Input (additional fields):**
```json
{
  "tool_name": "Bash",
  "tool_input": { "command": "npm test" },
  "tool_output": "output text"
}
```

**Output — block:**
```json
{
  "decision": "block",
  "reason": "reason"
}
```

---

### PostToolUseFailure

Fires after a tool call fails.

**Supports blocking:** Yes  
**Matchers:** tool name

**Input (additional fields):**
```json
{
  "tool_name": "Bash",
  "tool_input": { "command": "cmd" },
  "error": "error message"
}
```

---

### Stop

Fires when Claude finishes a response turn.

**Supports blocking:** Yes (returning `decision: block` continues the conversation)

**Output — inject feedback and continue:**
```json
{
  "hookSpecificOutput": {
    "hookEventName": "Stop",
    "additionalContext": "feedback injected back to Claude"
  }
}
```

---

### SessionStart

Fires when a session starts or resumes.

**Supports blocking:** No  
**Supported types:** `command`, `mcp_tool` only  
**Matchers:** `startup`, `resume`, `clear`, `compact`

**Input (additional fields):**
```json
{
  "source": "startup|resume|clear|compact",
  "model": "claude-sonnet-4-6",
  "session_title": "existing title"
}
```

**Output:**
```json
{
  "hookSpecificOutput": {
    "hookEventName": "SessionStart",
    "additionalContext": "context text",
    "sessionTitle": "new title",
    "watchPaths": ["/path/to/watch"],
    "reloadSkills": true,
    "initialUserMessage": "auto-sent first message"
  }
}
```

> Can write to `$CLAUDE_ENV_FILE` to persist env vars into the session.

---

### SessionEnd

Fires when the session terminates.

**Supports blocking:** No  
**Matchers:** `clear`, `resume`, `logout`, `prompt_input_exit`, etc.

---

### Notification

Fires when Claude Code sends a notification.

**Supports blocking:** No  
**Matchers:** notification type (`permission_prompt`, `auth_success`, `idle_prompt`, etc.)

**Input (additional fields):**
```json
{
  "notification_type": "permission_prompt",
  "message": "notification message"
}
```

---

### FileChanged

Fires when a watched file changes on disk. Watch paths declared via `SessionStart` hook's `watchPaths` output.

**Supports blocking:** No  
**Matchers:** literal filenames (e.g. `.env|.envrc`)

**Input (additional fields):**
```json
{
  "file_path": "/path/to/file",
  "change_type": "created|modified|deleted"
}
```

---

### PreCompact / PostCompact

Fires before/after context window compaction.

**Supports blocking:** `PreCompact` only  
**Matchers:** `manual`, `auto`

---

### MessageDisplay

Fires when assistant message text is displayed. Can replace the displayed text.

**Supports blocking:** No  
**Default timeout:** 10 seconds

**Output — replace displayed text:**
```json
{
  "hookSpecificOutput": {
    "hookEventName": "MessageDisplay",
    "displayContent": "replacement text"
  }
}
```

---

### Elicitation

Fires when an MCP server requests user input via the elicitation protocol.

**Supports blocking:** Yes  
**Matchers:** MCP server name

**Input (additional fields):**
```json
{
  "mcp_server": "server_name",
  "form_fields": [{ "name": "field1", "type": "text" }]
}
```

**Output — auto-fill form:**
```json
{
  "hookSpecificOutput": {
    "hookEventName": "Elicitation",
    "action": "accept|decline|cancel",
    "content": { "field1": "value" }
  }
}
```

---

## Matcher Patterns

| Pattern | Behavior |
|---|---|
| `"*"`, `""`, or omitted | Match everything |
| Letters/digits/`_`/`\|` | Exact match or `\|`-separated list (`Edit\|Write`) |
| Any other characters | Treated as JavaScript regex (`^mcp__.*`) |

---

## Handler Types Summary

| Type | Description |
|---|---|
| `command` | Shell script. `args` present → exec form; absent → `sh -c` shell form |
| `http` | POST to URL. Same JSON I/O contract as command hooks |
| `mcp_tool` | Call an MCP server tool. Tool output treated as stdout |
| `prompt` | Send prompt to Claude for yes/no decision |
| `agent` | Spawn subagent for tool-based verification |

---

## Async Hooks

```json
{ "type": "command", "command": "script.sh", "async": true }
```

Runs in background without blocking the turn.

```json
{ "type": "command", "command": "script.sh", "asyncRewake": true }
```

Runs in background; wakes Claude on exit code 2, showing stderr as a system reminder.

---

## Project Hook Wiring (this repo)

Current hooks configured in `.claude/settings.json`:

| Event | Hook | Status | What it does |
|---|---|---|---|
| `UserPromptSubmit` | `recall-memories.sh` | **BROKEN** | Calls old `/agentmemory/smart-search` (404 since route migration) |
| `UserPromptSubmit` | `agentmem-recall-hook` | Working | Calls `/v1/agentmemory/recall-summary`, injects memory summary as context |
| `PostToolUse` (Bash/Edit/Write/Read/…) | `observe-tool.sh` | Legacy | Observation hook (superseded) |
| `PostToolUse` (Bash/Edit/Write/Read/…) | `agentmem-tool-hook` | Working | Observes tool activity → `POST /v1/agentmemory/observe` |
| `PostToolUse` (Edit/Write) | `agentmem-md-procedural-hook` | Working | Extracts procedural memory from file edits |
| `Notification` | `notify.sh` | Legacy | Notify hook (superseded) |
| `Notification` | `agentmem-notify-hook` | Working | Logs notifications |
| `Stop` | `session-end.sh` | Legacy | Session end hook (superseded) |
| `Stop` | `agentmem-session-end-hook` | Working | Triggers consolidation pipeline |

### Known issues

1. **`recall-memories.sh` is broken** — calls `/agentmemory/smart-search` (no `/v1` prefix). Returns 404 silently. Should either be fixed to use `/v1/agentmemory/smart-search` or removed (redundant with `agentmem-recall-hook`).
2. **Hook output is plain text, not JSON** — `agentmem-recall-hook` prints `[agent-mem] ...` as plain text to stdout. The official spec expects `{"hookSpecificOutput": {"hookEventName": "UserPromptSubmit", "additionalContext": "..."}}`. Plain text is still injected by Claude Code as-is, but the JSON format is the correct contract.
3. **No relevance threshold in `agentmem-recall-hook`** — injects results regardless of relevance score, causing low-quality episodic noise (file reads, bash commands) to pollute context.
4. **`UserPromptExpansion` is unhooked** — slash command expansions are not fed through the memory recall pipeline.
