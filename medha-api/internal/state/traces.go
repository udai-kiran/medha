package state

import (
	"context"
	"encoding/json"
	"time"
)

// TraceRow is a reasoning trace record.
type TraceRow struct {
	ID          string
	SessionID   string
	Project     string
	Task        string
	StartedAt   string
	CompletedAt string
	Success     bool
	Outcome     string
	Steps       []*StepRow
}

// StepRow is a reasoning step within a trace.
type StepRow struct {
	ID          string
	TraceID     string
	Thought     string
	Action      string
	Observation string
	StepIndex   int
	CreatedAt   string
	ToolCalls   []*ToolCallRow
}

// ToolCallRow is a tool invocation within a reasoning step.
type ToolCallRow struct {
	ID              string
	StepID          string
	ToolName        string
	Arguments       map[string]any
	Result          map[string]any
	Status          string // success | error | partial
	ErrorMsg        string
	ExecutionTimeMs float64
	CreatedAt       string
}

// StartTrace creates a new reasoning trace.
func (s *Store) StartTrace(ctx context.Context, sessionID, project, task string, metadata map[string]any) (*TraceRow, error) {
	id := newID("trace")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	metaJSON, _ := json.Marshal(metadata)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO reasoning_traces (id, session_id, project, task, started_at, metadata_json)
         VALUES ($1, $2, $3, $4, $5, $6)`,
		id, sessionID, project, task, now, string(metaJSON))
	if err != nil {
		return nil, err
	}
	return &TraceRow{ID: id, SessionID: sessionID, Project: project, Task: task, StartedAt: now}, nil
}

// RecordStep appends a reasoning step to a trace.
func (s *Store) RecordStep(ctx context.Context, traceID, thought, action, observation string) (*StepRow, error) {
	// Get next index.
	var idx int
	_ = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(step_index)+1, 0) FROM reasoning_steps WHERE trace_id = $1`, traceID,
	).Scan(&idx)

	id := newID("step")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO reasoning_steps (id, trace_id, thought, action, observation, step_index, created_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, traceID, thought, action, observation, idx, now)
	if err != nil {
		return nil, err
	}
	return &StepRow{
		ID: id, TraceID: traceID, Thought: thought, Action: action,
		Observation: observation, StepIndex: idx, CreatedAt: now,
	}, nil
}

// RecordToolCall records a tool invocation within a step.
func (s *Store) RecordToolCall(ctx context.Context, stepID, toolName string, args, result map[string]any, status, errMsg string, execMs float64) (*ToolCallRow, error) {
	id := newID("tc")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	argsJSON, _ := json.Marshal(args)
	resultJSON, _ := json.Marshal(result)
	if status == "" {
		status = "success"
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO tool_calls (id, step_id, tool_name, arguments_json, result_json, status, error_msg, execution_time_ms, created_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, stepID, toolName, string(argsJSON), string(resultJSON), status, errMsg, execMs, now)
	if err != nil {
		return nil, err
	}
	return &ToolCallRow{
		ID: id, StepID: stepID, ToolName: toolName, Arguments: args,
		Result: result, Status: status, ErrorMsg: errMsg,
		ExecutionTimeMs: execMs, CreatedAt: now,
	}, nil
}

// CompleteTrace marks a trace as done with an outcome.
func (s *Store) CompleteTrace(ctx context.Context, id, outcome string, success bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE reasoning_traces SET completed_at = $1, outcome = $2, success = $3 WHERE id = $4`,
		now, outcome, successInt, id)
	return err
}

// GetTrace returns a trace with all its steps and tool calls.
func (s *Store) GetTrace(ctx context.Context, id string) (*TraceRow, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, session_id, COALESCE(project,''), task, started_at,
                COALESCE(completed_at,''), success, COALESCE(outcome,'')
         FROM reasoning_traces WHERE id = $1`, id)
	var t TraceRow
	var successInt int
	if err := row.Scan(&t.ID, &t.SessionID, &t.Project, &t.Task, &t.StartedAt,
		&t.CompletedAt, &successInt, &t.Outcome); err != nil {
		return nil, ErrNotFound
	}
	t.Success = successInt != 0

	srows, err := s.DB.QueryContext(ctx,
		`SELECT id, trace_id, thought, COALESCE(action,''), COALESCE(observation,''), step_index, created_at
         FROM reasoning_steps WHERE trace_id = $1 ORDER BY step_index`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = srows.Close() }()
	for srows.Next() {
		step := &StepRow{}
		if err := srows.Scan(&step.ID, &step.TraceID, &step.Thought, &step.Action,
			&step.Observation, &step.StepIndex, &step.CreatedAt); err != nil {
			return nil, err
		}
		t.Steps = append(t.Steps, step)
	}
	return &t, nil
}

// SearchTraces returns traces matching a query (task text search).
func (s *Store) SearchTraces(ctx context.Context, project, query string, limit int) ([]*TraceRow, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + query + "%"
	rows, err := s.DB.QueryContext(ctx, `
        SELECT id, session_id, COALESCE(project,''), task, started_at,
               COALESCE(completed_at,''), success, COALESCE(outcome,'')
        FROM reasoning_traces
        WHERE ($1 = '' OR project = $2) AND LOWER(task) LIKE LOWER($3)
        ORDER BY started_at DESC LIMIT $4
    `, project, project, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*TraceRow
	for rows.Next() {
		t := &TraceRow{}
		var successInt int
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Project, &t.Task, &t.StartedAt,
			&t.CompletedAt, &successInt, &t.Outcome); err != nil {
			return nil, err
		}
		t.Success = successInt != 0
		out = append(out, t)
	}
	return out, rows.Err()
}
