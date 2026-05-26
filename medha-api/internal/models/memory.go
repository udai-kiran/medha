package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MemoryType is the kind of distilled fact a Memory holds.
type MemoryType string

const (
	MemoryArchitecture MemoryType = "architecture"
	MemoryPattern      MemoryType = "pattern"
	MemoryPreference   MemoryType = "preference"
	MemoryBug          MemoryType = "bug"
	MemoryWorkflow     MemoryType = "workflow"
	MemoryFact         MemoryType = "fact"
)

var validMemoryTypes = map[MemoryType]struct{}{
	MemoryArchitecture: {}, MemoryPattern: {}, MemoryPreference: {},
	MemoryBug: {}, MemoryWorkflow: {}, MemoryFact: {},
}

// IsValid reports whether t is a known memory type.
func (t MemoryType) IsValid() bool { _, ok := validMemoryTypes[t]; return ok }

// MemoryTier matches the 4-tier model in Task 23.
type MemoryTier string

const (
	TierWorking    MemoryTier = "working"
	TierEpisodic   MemoryTier = "episodic"
	TierSemantic   MemoryTier = "semantic"
	TierProcedural MemoryTier = "procedural"
)

var validTiers = map[MemoryTier]struct{}{
	TierWorking: {}, TierEpisodic: {}, TierSemantic: {}, TierProcedural: {},
}

// IsValid reports whether t is a known memory tier.
func (t MemoryTier) IsValid() bool { _, ok := validTiers[t]; return ok }

// MarshalJSON keeps memory enum types strict on the wire.
func (t MemoryType) MarshalJSON() ([]byte, error) {
	if !t.IsValid() {
		return nil, fmt.Errorf("models: invalid MemoryType %q", string(t))
	}
	return json.Marshal(string(t))
}

// UnmarshalJSON rejects unknown values.
func (t *MemoryType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v := MemoryType(s)
	if !v.IsValid() {
		return fmt.Errorf("models: unknown MemoryType %q", s)
	}
	*t = v
	return nil
}

// MarshalJSON keeps tier enum strict on the wire.
func (t MemoryTier) MarshalJSON() ([]byte, error) {
	if !t.IsValid() {
		return nil, fmt.Errorf("models: invalid MemoryTier %q", string(t))
	}
	return json.Marshal(string(t))
}

// UnmarshalJSON rejects unknown values.
func (t *MemoryTier) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v := MemoryTier(s)
	if !v.IsValid() {
		return fmt.Errorf("models: unknown MemoryTier %q", s)
	}
	*t = v
	return nil
}

// Memory is a reusable, decaying fact distilled from one or more observations.
type Memory struct {
	ID                   string     `json:"id"`
	Project              string     `json:"project,omitempty"`
	Type                 MemoryType `json:"type"`
	Tier                 MemoryTier `json:"tier"`
	Title                string     `json:"title"`
	Content              string     `json:"content"`
	Concepts             []string   `json:"concepts,omitempty"`
	Files                []string   `json:"files,omitempty"`
	SessionIDs           []string   `json:"sessionIds,omitempty"`
	SourceObservationIDs []string   `json:"sourceObservationIds,omitempty"`
	Strength             float64    `json:"strength"`
	IsLatest             bool       `json:"isLatest"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	LastRetrievedAt      *time.Time `json:"lastRetrievedAt,omitempty"`
}

// Validate enforces required fields and reasonable strength.
func (m *Memory) Validate() error {
	if m == nil {
		return errors.New("Memory: nil")
	}
	if m.ID == "" {
		return errors.New("Memory: id required")
	}
	if !m.Type.IsValid() {
		return fmt.Errorf("Memory: invalid type %q", m.Type)
	}
	if !m.Tier.IsValid() {
		return fmt.Errorf("Memory: invalid tier %q", m.Tier)
	}
	if m.Title == "" {
		return errors.New("Memory: title required")
	}
	if m.Strength < 0 || m.Strength > 1 {
		return fmt.Errorf("Memory: strength out of [0,1]: %v", m.Strength)
	}
	return nil
}

// SessionSummary is produced by Task 21/22 at SessionEnd.
type SessionSummary struct {
	SessionID     string    `json:"sessionId"`
	Title         string    `json:"title"`
	Narrative     string    `json:"narrative"`
	KeyDecisions  []string  `json:"keyDecisions,omitempty"`
	FilesModified []string  `json:"filesModified,omitempty"`
	Concepts      []string  `json:"concepts,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}
