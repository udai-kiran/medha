// Command hooks is a single-binary Claude Code hook dispatcher.
// It reads JSON from stdin, dispatches on hook_event_name, writes the
// appropriate output to stdout, then exits. A global WaitGroup drains
// async observation goroutines before exit (timeout: 3 s; 10 s for Stop).
// A top-level recover ensures hooks never block the harness on panic.
package main

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

var asyncWG sync.WaitGroup

func main() {
	defer func() {
		if r := recover(); r != nil {
			os.Stdout.Write([]byte("{}\n")) //nolint:errcheck
			os.Exit(0)
		}
	}()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil || len(raw) == 0 {
		os.Stdout.Write([]byte("{}\n")) //nolint:errcheck
		return
	}

	var base BaseInput
	if err := json.Unmarshal(raw, &base); err != nil {
		os.Stdout.Write([]byte("{}\n")) //nolint:errcheck
		return
	}

	cwd := base.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	cfg := loadConfig(cwd)

	out := dispatch(base.HookEventName, cfg, cwd, raw, base)

	// Write output first so Claude Code gets it without waiting for drain.
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		os.Stdout.Write([]byte("{}\n")) //nolint:errcheck
	}

	// Drain async observations. Stop / SessionEnd get a longer window so the
	// consolidation pipeline observes the session_end before the process exits.
	timeout := 3 * time.Second
	if base.HookEventName == "Stop" || base.HookEventName == "SessionEnd" {
		timeout = 10 * time.Second
	}
	done := make(chan struct{})
	go func() { asyncWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func dispatch(event string, cfg config, cwd string, raw []byte, base BaseInput) any {
	switch event {
	case "PreToolUse":
		return handlePreToolUse(cfg, cwd, raw, base)
	case "PostToolUse":
		return handlePostToolUse(cfg, cwd, raw, base)
	case "PostToolUseFailure":
		return handlePostToolUseFailure(cfg, cwd, raw, base)
	case "PostToolBatch":
		return handlePostToolBatch(cfg, cwd, raw, base)
	case "PermissionRequest":
		return handlePermissionRequest(cfg, cwd, raw, base)
	case "PermissionDenied":
		return handlePermissionDenied(cfg, cwd, raw, base)
	case "Notification":
		return handleNotification(cfg, cwd, raw, base)
	case "UserPromptSubmit":
		return handleUserPromptSubmit(cfg, cwd, raw, base)
	case "UserPromptExpansion":
		return handleUserPromptExpansion(cfg, cwd, raw, base)
	case "SessionStart":
		return handleSessionStart(cfg, cwd, raw, base)
	case "Stop":
		return handleStop(cfg, cwd, raw, base)
	case "StopFailure":
		return handleStopFailure(cfg, cwd, raw, base)
	case "SubagentStart":
		return handleSubagentStart(cfg, cwd, raw, base)
	case "SubagentStop":
		return handleSubagentStop(cfg, cwd, raw, base)
	case "PreCompact":
		return handlePreCompact(cfg, cwd, raw, base)
	case "PostCompact":
		return handlePostCompact(cfg, cwd, raw, base)
	case "SessionEnd":
		return handleSessionEnd(cfg, cwd, raw, base)
	case "InstructionsLoaded":
		return handleInstructionsLoaded(cfg, cwd, raw, base)
	case "ConfigChange":
		return handleConfigChange(cfg, cwd, raw, base)
	case "CwdChanged":
		return handleCwdChanged(cfg, cwd, raw, base)
	case "FileChanged":
		return handleFileChanged(cfg, cwd, raw, base)
	case "WorktreeCreate":
		return handleWorktreeCreate(cfg, cwd, raw, base)
	case "WorktreeRemove":
		return handleWorktreeRemove(cfg, cwd, raw, base)
	case "Elicitation":
		return handleElicitation(cfg, cwd, raw, base)
	case "ElicitationResult":
		return handleElicitationResult(cfg, cwd, raw, base)
	case "TeammateIdle":
		return handleTeammateIdle(cfg, cwd, raw, base)
	case "TaskCreated":
		return handleTaskCreated(cfg, cwd, raw, base)
	case "TaskCompleted":
		return handleTaskCompleted(cfg, cwd, raw, base)
	case "MessageDisplay":
		return handleMessageDisplay(cfg, cwd, raw, base)
	case "Setup":
		return handleSetup(cfg, cwd, raw, base)
	default:
		return handleUnknown(cfg, cwd, raw, base)
	}
}
