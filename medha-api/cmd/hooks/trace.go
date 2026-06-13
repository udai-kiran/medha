package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

// TraceState tracks per-session counters in /tmp so concurrent hook processes
// share a consistent view of the session. All reads and writes go through
// withTraceLock which holds an exclusive flock for atomicity.
type TraceState struct {
	Turn             int      `json:"turn"`
	TranscriptOffset int64    `json:"transcript_offset"`
	InjectedIDs      []string `json:"injected_ids,omitempty"`
}

func tracePath(sessionID string) string {
	return filepath.Join(os.TempDir(), "agentmem-trace-"+sanitizeID(sessionID)+".json")
}

func traceLockPath(sessionID string) string {
	return filepath.Join(os.TempDir(), "agentmem-trace-"+sanitizeID(sessionID)+".lock")
}

// withTraceLock acquires an exclusive flock, loads state, calls fn, then
// persists the (possibly modified) state. Concurrent hook processes block on
// the flock rather than racing on the state file.
func withTraceLock(sessionID string, fn func(*TraceState)) {
	if sessionID == "" {
		return
	}
	lf, err := os.OpenFile(traceLockPath(sessionID), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer lf.Close()
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

// deleteTraceState removes all trace files for a session (called at SessionEnd).
func deleteTraceState(sessionID string) {
	if sessionID == "" {
		return
	}
	os.Remove(tracePath(sessionID))     //nolint:errcheck
	os.Remove(traceLockPath(sessionID)) //nolint:errcheck
}
