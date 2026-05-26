package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Modality is the medium an observation carries.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityMixed Modality = "mixed"
)

// IsValid reports whether m is a known modality.
func (m Modality) IsValid() bool {
	return m == ModalityText || m == ModalityImage || m == ModalityMixed
}

// RawObservation is the pre-compression form: validated, filtered, persisted
// by Task 8. It is the entity Python's compressor consumes.
type RawObservation struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionId"`
	Project    string          `json:"project,omitempty"`
	Timestamp  time.Time       `json:"timestamp"`
	HookType   HookType        `json:"hookType"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolInput  json.RawMessage `json:"toolInput,omitempty"`
	ToolOutput string          `json:"toolOutput,omitempty"`
	UserPrompt string          `json:"userPrompt,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	Modality   Modality        `json:"modality"`
	ImageRef   string          `json:"imageRef,omitempty"`
	// HasSecrets is set by the privacy filter (Task 10) and read by enrichment
	// to skip external lookups for sensitive entities (FR-9).
	HasSecrets bool `json:"hasSecrets,omitempty"`
}

// Validate enforces required fields. Run after privacy filtering but before
// persistence (Task 8).
func (o *RawObservation) Validate() error {
	if o == nil {
		return errors.New("RawObservation: nil")
	}
	if o.ID == "" {
		return errors.New("RawObservation: id required")
	}
	if o.SessionID == "" {
		return errors.New("RawObservation: sessionId required")
	}
	if !o.HookType.IsValid() {
		return fmt.Errorf("RawObservation: invalid hookType %q", string(o.HookType))
	}
	if o.Modality == "" {
		o.Modality = ModalityText
	} else if !o.Modality.IsValid() {
		return fmt.Errorf("RawObservation: invalid modality %q", string(o.Modality))
	}
	if o.Timestamp.IsZero() {
		return errors.New("RawObservation: timestamp required")
	}
	return nil
}

// CompressedObservation is the searchable form produced by the compression
// pipeline (Task 11 synthetic / Task 13 LLM). Shares its id with the
// originating RawObservation.
type CompressedObservation struct {
	ID               string   `json:"id"`
	SessionID        string   `json:"sessionId"`
	Type             string   `json:"type"`               // file_read | file_edit | command | search | ...
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle,omitempty"`
	Facts            []string `json:"facts,omitempty"`
	Narrative        string   `json:"narrative,omitempty"`
	Concepts         []string `json:"concepts,omitempty"`
	Files            []string `json:"files,omitempty"`
	Importance       int      `json:"importance"`         // 0..10
	Confidence       float64  `json:"confidence"`         // 0..1; lower for synthetic
	ImageDescription string   `json:"imageDescription,omitempty"`
}

// Validate sanity-checks the compressed form. Returned errors here typically
// indicate a buggy compressor, not a user error.
func (c *CompressedObservation) Validate() error {
	if c == nil {
		return errors.New("CompressedObservation: nil")
	}
	if c.ID == "" {
		return errors.New("CompressedObservation: id required")
	}
	if c.SessionID == "" {
		return errors.New("CompressedObservation: sessionId required")
	}
	if c.Type == "" {
		return errors.New("CompressedObservation: type required")
	}
	if c.Title == "" {
		return errors.New("CompressedObservation: title required")
	}
	if c.Importance < 0 || c.Importance > 10 {
		return fmt.Errorf("CompressedObservation: importance out of [0,10]: %d", c.Importance)
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return fmt.Errorf("CompressedObservation: confidence out of [0,1]: %v", c.Confidence)
	}
	return nil
}
