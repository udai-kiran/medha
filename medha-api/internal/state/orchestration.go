package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Orchestration primitives live in KV scopes (Actions, Leases, Routines,
// Signals — declared in kv.go). This file provides typed accessors so
// handlers don't sling JSON blobs directly.

// ActionRow is the storage shape of a multi-agent Action.
type ActionRow struct {
	ID           string         `json:"id"`
	Project      string         `json:"project,omitempty"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	Status       string         `json:"status"`
	Dependencies []string       `json:"dependencies,omitempty"`
	AssigneeID   string         `json:"assigneeId,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// PutAction writes/updates an Action.
func (s *Store) PutAction(ctx context.Context, a *ActionRow) error {
	if a == nil || a.ID == "" {
		return errors.New("PutAction: id required")
	}
	if a.Status == "" {
		a.Status = "pending"
	}
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	kv := NewKV(s)
	return kv.Put(ctx, ScopeActions, Key(ScopeActions, a.Project, a.ID), a)
}

// GetAction reads an Action by id.
func (s *Store) GetAction(ctx context.Context, project, id string) (*ActionRow, error) {
	kv := NewKV(s)
	var a ActionRow
	if err := kv.Get(ctx, ScopeActions, Key(ScopeActions, project, id), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListActions returns every Action under a project.
func (s *Store) ListActions(ctx context.Context, project string) ([]*ActionRow, error) {
	kv := NewKV(s)
	pairs, err := kv.ListByPrefix(ctx, ScopeActions, Key(ScopeActions, project, "")+":")
	if err != nil {
		return nil, err
	}
	out := make([]*ActionRow, 0, len(pairs))
	for _, raw := range pairs {
		var a ActionRow
		if err := json.Unmarshal([]byte(raw), &a); err == nil {
			out = append(out, &a)
		}
	}
	return out, nil
}

// Frontier returns Actions in `pending` whose dependencies are all completed
// — the set safely claimable next.
func (s *Store) Frontier(ctx context.Context, project string) ([]*ActionRow, error) {
	actions, err := s.ListActions(ctx, project)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*ActionRow, len(actions))
	for _, a := range actions {
		byID[a.ID] = a
	}
	out := make([]*ActionRow, 0, len(actions))
	for _, a := range actions {
		if a.Status != "pending" {
			continue
		}
		ready := true
		for _, dep := range a.Dependencies {
			if d, ok := byID[dep]; !ok || d.Status != "completed" {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, a)
		}
	}
	return out, nil
}

// LeaseRow records exclusive ownership of an Action by an agent/holder.
type LeaseRow struct {
	ActionID  string    `json:"actionId"`
	HolderID  string    `json:"holderId"`
	GrantedAt time.Time `json:"grantedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// AcquireLease atomically grants a lease for actionID to holderID with the
// given TTL. Returns ErrLeaseHeld if another holder still has a live lease.
var ErrLeaseHeld = errors.New("state: action already leased")

// AcquireLease performs the check-and-set under a single transaction.
func (s *Store) AcquireLease(ctx context.Context, project, actionID, holderID string, ttl time.Duration) (*LeaseRow, error) {
	if actionID == "" || holderID == "" {
		return nil, errors.New("AcquireLease: actionID and holderID required")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	key := Key(ScopeLeases, project, actionID)
	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT value_json FROM kv WHERE scope = $1 AND key = $2`,
		string(ScopeLeases), key,
	).Scan(&existing)

	if err == nil {
		var prev LeaseRow
		_ = json.Unmarshal([]byte(existing), &prev)
		if now.Before(prev.ExpiresAt) && prev.HolderID != holderID {
			return nil, ErrLeaseHeld
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	lease := &LeaseRow{
		ActionID:  actionID,
		HolderID:  holderID,
		GrantedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	blob, _ := json.Marshal(lease)
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO kv (scope, key, value_json, updated_at)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT(scope, key) DO UPDATE SET
            value_json = excluded.value_json,
            updated_at = excluded.updated_at
    `, string(ScopeLeases), key, string(blob), now.Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

// ReleaseLease removes the lease for an action. Idempotent.
func (s *Store) ReleaseLease(ctx context.Context, project, actionID, holderID string) error {
	if actionID == "" {
		return errors.New("ReleaseLease: actionID required")
	}
	kv := NewKV(s)
	var existing LeaseRow
	if err := kv.Get(ctx, ScopeLeases, Key(ScopeLeases, project, actionID), &existing); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if existing.HolderID != "" && holderID != "" && existing.HolderID != holderID {
		return errors.New("ReleaseLease: not the holder")
	}
	return kv.Delete(ctx, ScopeLeases, Key(ScopeLeases, project, actionID))
}

// SignalRow is an inter-agent message with optional read receipts.
type SignalRow struct {
	ID          string         `json:"id"`
	Project     string         `json:"project,omitempty"`
	From        string         `json:"from"`
	To          string         `json:"to"`
	Subject     string         `json:"subject"`
	Body        string         `json:"body"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	DeliveredAt *time.Time     `json:"deliveredAt,omitempty"`
}

// SendSignal writes a SignalRow under both the recipient's and the sender's
// indexes so either side can list.
func (s *Store) SendSignal(ctx context.Context, sig *SignalRow) error {
	if sig == nil || sig.From == "" || sig.To == "" || sig.ID == "" {
		return errors.New("SendSignal: id, from, to required")
	}
	if sig.CreatedAt.IsZero() {
		sig.CreatedAt = time.Now().UTC()
	}
	kv := NewKV(s)
	// recipient view first; sender view second so a partial write favours delivery.
	if err := kv.Put(ctx, ScopeSignals, Key(ScopeSignals, sig.Project, fmt.Sprintf("inbox:%s:%s", sig.To, sig.ID)), sig); err != nil {
		return err
	}
	return kv.Put(ctx, ScopeSignals, Key(ScopeSignals, sig.Project, fmt.Sprintf("outbox:%s:%s", sig.From, sig.ID)), sig)
}

// ListInbox returns signals delivered to `to` (most recent first).
func (s *Store) ListInbox(ctx context.Context, project, to string) ([]*SignalRow, error) {
	kv := NewKV(s)
	prefix := Key(ScopeSignals, project, "inbox:"+to)
	pairs, err := kv.ListByPrefix(ctx, ScopeSignals, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*SignalRow, 0, len(pairs))
	for _, raw := range pairs {
		var sg SignalRow
		if err := json.Unmarshal([]byte(raw), &sg); err == nil {
			out = append(out, &sg)
		}
	}
	return out, nil
}

// RoutineRow is a reusable multi-agent workflow template.
type RoutineRow struct {
	ID          string   `json:"id"`
	Project     string   `json:"project,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Steps       []string `json:"steps,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// PutRoutine writes a routine template.
func (s *Store) PutRoutine(ctx context.Context, r *RoutineRow) error {
	if r == nil || r.ID == "" || r.Name == "" {
		return errors.New("PutRoutine: id and name required")
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	kv := NewKV(s)
	return kv.Put(ctx, ScopeRoutines, Key(ScopeRoutines, r.Project, r.ID), r)
}

// ListRoutines returns all routines under a project.
func (s *Store) ListRoutines(ctx context.Context, project string) ([]*RoutineRow, error) {
	kv := NewKV(s)
	pairs, err := kv.ListByPrefix(ctx, ScopeRoutines, Key(ScopeRoutines, project, "")+":")
	if err != nil {
		return nil, err
	}
	// project may be empty → strip the trailing colon prefix.
	if project == "" {
		pairs, err = kv.ListByPrefix(ctx, ScopeRoutines, string(ScopeRoutines)+":")
		if err != nil {
			return nil, err
		}
	}
	out := make([]*RoutineRow, 0, len(pairs))
	for _, raw := range pairs {
		var r RoutineRow
		if err := json.Unmarshal([]byte(raw), &r); err == nil {
			out = append(out, &r)
		}
	}
	return out, nil
}

// SignalID is a small helper for callers that don't have a uuid handy.
func SignalID() string {
	var b [6]byte
	now := time.Now().UnixNano()
	for i := 0; i < 6; i++ {
		b[i] = byte(now >> (i * 8))
	}
	return fmt.Sprintf("sig-%012x", now&0xFFFFFFFFFFFF)
}

// CreateCheckpoint registers a condition gate (G20).
func (s *Store) CreateCheckpoint(ctx context.Context, project, id, conditionExpr string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO checkpoints (id, project, condition_expr, created_at)
         VALUES ($1, $2, $3, $4) ON CONFLICT(id) DO NOTHING`, id, project, conditionExpr, now)
	return err
}

// SatisfyCheckpoint marks a checkpoint as satisfied.
func (s *Store) SatisfyCheckpoint(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE checkpoints SET satisfied_at = $1 WHERE id = $2`, now, id)
	return err
}

// CreateSentinel registers an event-driven watcher (G20).
func (s *Store) CreateSentinel(ctx context.Context, project, id, eventPattern, handlerURL string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sentinels (id, project, event_pattern, handler_url, created_at)
         VALUES ($1, $2, $3, $4, $5) ON CONFLICT(id) DO NOTHING`, id, project, eventPattern, handlerURL, now)
	return err
}

// TriggerSentinel fires a sentinel by id (G20). Returns the handler URL.
func (s *Store) TriggerSentinel(ctx context.Context, id string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var handlerURL string
	err := s.DB.QueryRowContext(ctx,
		`UPDATE sentinels SET triggered_at = $1 WHERE id = $2 RETURNING handler_url`, now, id,
	).Scan(&handlerURL)
	return handlerURL, err
}

// SketchRow is an ephemeral in-memory action graph (G21).
// Stored in KV so it is session-scoped and not persisted long-term.
type SketchRow struct {
	ID      string         `json:"id"`
	Project string         `json:"project"`
	Actions []ActionRow    `json:"actions"`
	Params  map[string]any `json:"params,omitempty"`
}

// CreateSketch stores an ephemeral sketch in KV.
func (s *Store) CreateSketch(ctx context.Context, sketch *SketchRow) error {
	if sketch.ID == "" {
		return errors.New("CreateSketch: id required")
	}
	kv := NewKV(s)
	return kv.Put(ctx, ScopeActions, Key(ScopeActions, sketch.Project, "sketch:"+sketch.ID), sketch)
}

// PromoteSketch converts a sketch to a permanent routine.
func (s *Store) PromoteSketch(ctx context.Context, project, sketchID string) (*RoutineRow, error) {
	kv := NewKV(s)
	var sketch SketchRow
	if err := kv.Get(ctx, ScopeActions, Key(ScopeActions, project, "sketch:"+sketchID), &sketch); err != nil {
		return nil, err
	}
	steps := make([]string, 0, len(sketch.Actions))
	for _, a := range sketch.Actions {
		steps = append(steps, a.Title)
	}
	routine := &RoutineRow{
		ID: newID("routine"), Project: project,
		Name:        fmt.Sprintf("sketch-%s", sketchID),
		Description: fmt.Sprintf("Promoted from sketch %s", sketchID),
		Steps:       steps,
	}
	if err := s.PutRoutine(ctx, routine); err != nil {
		return nil, err
	}
	_ = kv.Delete(ctx, ScopeActions, Key(ScopeActions, project, "sketch:"+sketchID))
	return routine, nil
}

// Crystallize compacts a list of completed action IDs into a single summary action (G21).
func (s *Store) Crystallize(ctx context.Context, project string, actionIDs []string) (*ActionRow, error) {
	titles := make([]string, 0, len(actionIDs))
	for _, id := range actionIDs {
		a, err := s.GetAction(ctx, project, id)
		if err == nil {
			titles = append(titles, a.Title)
		}
	}
	summary := &ActionRow{
		ID:          newID("cryst"),
		Project:     project,
		Title:       fmt.Sprintf("Crystallized: %d actions", len(titles)),
		Description: strings.Join(titles, "; "),
		Status:      "completed",
	}
	if err := s.PutAction(ctx, summary); err != nil {
		return nil, err
	}
	return summary, nil
}

// GetFrontier wraps Frontier for compatibility with new API surface (G16).
func (s *Store) GetFrontier(ctx context.Context, project string) ([]*ActionRow, error) {
	return s.Frontier(ctx, project)
}

// GetNextAction returns the single highest-priority unblocked action (G16).
func (s *Store) GetNextAction(ctx context.Context, project string) (*ActionRow, error) {
	actions, err := s.Frontier(ctx, project)
	if err != nil || len(actions) == 0 {
		return nil, err
	}
	return actions[0], nil
}

// ScrubLeases removes expired leases; called periodically by Task 24's
// scheduler if wired (out of scope for M6 baseline — leases also expire
// implicitly via AcquireLease's check).
func (s *Store) ScrubLeases(ctx context.Context, project string) (int, error) {
	kv := NewKV(s)
	prefix := Key(ScopeLeases, project, "")
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	pairs, err := kv.ListByPrefix(ctx, ScopeLeases, prefix)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	n := 0
	for k, raw := range pairs {
		var lease LeaseRow
		if json.Unmarshal([]byte(raw), &lease) != nil {
			continue
		}
		if now.After(lease.ExpiresAt) {
			if err := kv.Delete(ctx, ScopeLeases, k); err == nil {
				n++
			}
		}
	}
	return n, nil
}
