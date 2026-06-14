package main

import (
	"encoding/json"
	"os"
)

// debugAppend appends one JSON line to the file at path.
// Silently no-ops on any error so debug logging never affects hook behaviour.
func debugAppend(path string, record any) {
	if path == "" {
		return
	}
	line, err := json.Marshal(record)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()           //nolint:errcheck
	f.Write(append(line, '\n')) //nolint:errcheck
}
