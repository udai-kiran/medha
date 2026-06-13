package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// TraceState tracks per-session counters in /tmp so concurrent hook processes
// share a consistent view of the session. All reads and writes go through
// withTraceLock which holds an exclusive flock for atomicity.
type TraceState struct {
	Turn        int      `json:"turn"`
	InjectedIDs []string `json:"injected_ids,omitempty"`
}

func tracePath(sessionID string) string {
	return filepath.Join(os.TempDir(), "agentmem-codex-trace-"+sanitizeID(sessionID)+".json")
}

func traceLockPath(sessionID string) string {
	return filepath.Join(os.TempDir(), "agentmem-codex-trace-"+sanitizeID(sessionID)+".lock")
}

// withTraceLock acquires an exclusive flock, loads state, calls fn, then
// persists the (possibly modified) state.
func withTraceLock(sessionID string, fn func(*TraceState)) {
	if sessionID == "" {
		return
	}
	lf, err := os.OpenFile(traceLockPath(sessionID), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer lf.Close() //nolint:errcheck
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck

	path := tracePath(sessionID)
	var state TraceState
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &state) //nolint:errcheck
	}
	fn(&state)
	if data, err := json.Marshal(state); err == nil {
		os.WriteFile(path, data, 0600) //nolint:errcheck
	}
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
