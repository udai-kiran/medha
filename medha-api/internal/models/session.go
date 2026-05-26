package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// SessionStatus enumerates the session lifecycle states.
type SessionStatus string

const (
	SessionActive    SessionStatus = "active"
	SessionCompleted SessionStatus = "completed"
	SessionAbandoned SessionStatus = "abandoned"
)

var validSessionStatuses = map[SessionStatus]struct{}{
	SessionActive: {}, SessionCompleted: {}, SessionAbandoned: {},
}

// IsValid reports whether s is a known session status.
func (s SessionStatus) IsValid() bool { _, ok := validSessionStatuses[s]; return ok }

// MarshalJSON keeps session status enum strict on the wire.
func (s SessionStatus) MarshalJSON() ([]byte, error) {
	if !s.IsValid() {
		return nil, fmt.Errorf("models: invalid SessionStatus %q", string(s))
	}
	return json.Marshal(string(s))
}

// UnmarshalJSON rejects unknown values.
func (s *SessionStatus) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	v := SessionStatus(str)
	if !v.IsValid() {
		return fmt.Errorf("models: unknown SessionStatus %q", str)
	}
	*s = v
	return nil
}

// Session is the lifecycle view: a window during which an agent emits hooks.
type Session struct {
	ID               string        `json:"id"`
	Project          string        `json:"project,omitempty"`
	CWD              string        `json:"cwd,omitempty"`
	Status           SessionStatus `json:"status"`
	ObservationCount int           `json:"observationCount"`
	Tags             []string      `json:"tags,omitempty"`
	Summary          string        `json:"summary,omitempty"`
	StartedAt        time.Time     `json:"startedAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	EndedAt          *time.Time    `json:"endedAt,omitempty"`
}
