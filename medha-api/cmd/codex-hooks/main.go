// Command codex-hooks is a single-binary Codex hook dispatcher.
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
			os.Stdout.Write([]byte("{}\n"))
			os.Exit(0)
		}
	}()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil || len(raw) == 0 {
		os.Stdout.Write([]byte("{}\n"))
		return
	}

	var base BaseInput
	if err := json.Unmarshal(raw, &base); err != nil {
		os.Stdout.Write([]byte("{}\n"))
		return
	}

	cwd := base.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	cfg := loadConfig(cwd)

	out := dispatch(base.HookEventName, cfg, cwd, raw, base)

	// Write output first so Codex gets it without waiting for drain.
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		os.Stdout.Write([]byte("{}\n"))
	}

	// Drain async observations. Stop gets a longer window so the checkpoint
	// observation arrives before the process exits.
	timeout := 3 * time.Second
	if base.HookEventName == "Stop" {
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
	case "SessionStart":
		return handleSessionStart(cfg, cwd, raw, base)
	case "UserPromptSubmit":
		return handleUserPromptSubmit(cfg, cwd, raw, base)
	case "PreToolUse":
		return handlePreToolUse(cfg, cwd, raw, base)
	case "PostToolUse":
		return handlePostToolUse(cfg, cwd, raw, base)
	case "PermissionRequest":
		return handlePermissionRequest(cfg, cwd, raw, base)
	case "PreCompact":
		return handlePreCompact(cfg, cwd, raw, base)
	case "PostCompact":
		return handlePostCompact(cfg, cwd, raw, base)
	case "SubagentStart":
		return handleSubagentStart(cfg, cwd, raw, base)
	case "SubagentStop":
		return handleSubagentStop(cfg, cwd, raw, base)
	case "Stop":
		return handleStop(cfg, cwd, raw, base)
	default:
		return empty{}
	}
}
