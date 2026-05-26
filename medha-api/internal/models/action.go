package models

import "time"

// Action is a node in the multi-agent orchestration DAG (Task 31).
// Defined here so all packages can reference the type; behaviour (graph
// traversal, frontier, next-step) lives with Task 31.
type Action struct {
	ID           string         `json:"id"`
	Project      string         `json:"project,omitempty"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	Status       string         `json:"status"` // pending | running | completed | failed | cancelled
	Dependencies []string       `json:"dependencies,omitempty"`
	AssigneeID   string         `json:"assigneeId,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// Lease records exclusive ownership of an Action by an agent (Task 31).
type Lease struct {
	ActionID  string    `json:"actionId"`
	HolderID  string    `json:"holderId"` // agent or user id
	GrantedAt time.Time `json:"grantedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// IsExpired returns true when the lease should be considered up for grabs.
func (l *Lease) IsExpired(now time.Time) bool {
	return l == nil || !now.Before(l.ExpiresAt)
}
