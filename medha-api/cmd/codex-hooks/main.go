// Command codex-hooks is a single-binary Codex hook dispatcher.
// It reads JSON from stdin, dispatches on hook_event_name, writes the
// appropriate output to stdout, then exits. Pure-capture events exit
// immediately without draining async goroutines (fire-and-forget). Events
// that inject context or must guarantee delivery drain with a short timeout.
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

	// Pure-capture events exit immediately — there is no injection and the
	// observation is truly fire-and-forget. Draining here would add up to
	// observeTimeout latency on every tool call when the service is unreachable.
	if captureOnly(base.HookEventName) {
		return
	}

	// For events that inject context or trigger consolidation (Stop fires
	// session_end), give goroutines a bounded window to complete.
	// Stop gets a longer window because it sends two observations.
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

// captureOnly reports whether event only fires async capture and needs no drain.
// These events return {} immediately; blocking on goroutine completion would add
// observeClient.Timeout latency on every call when the service is unreachable.
func captureOnly(event string) bool {
	switch event {
	case "PreToolUse", "PostToolUse", "PermissionRequest",
		"PreCompact", "SubagentStart", "SubagentStop":
		return true
	}
	return false
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
