package consolidation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// WorkerConfig configures the consumer process started by cmd/worker.
type WorkerConfig struct {
	// PythonServiceURL is the base URL of the Python service (e.g. http://py:5000).
	PythonServiceURL string
	// InternalCallbackURL is the Go service's base URL for the
	// /internal/observation/{id}/compressed callback (e.g. http://go:3111).
	InternalCallbackURL string
	// HTTPTimeout caps each outbound HTTP call.
	HTTPTimeout time.Duration
	// Logger is required.
	Logger *slog.Logger
}

// Worker processes jobs pulled from a Queue. The compress path:
//  1. Fetch the raw observation by id from Go (TODO: Task 13 adds the endpoint
//     OR the worker reads SQLite directly via a state.Store reference — for
//     now, the job payload already carries everything Python needs).
//  2. POST /compress on Python.
//  3. POST /internal/observation/{id}/compressed on Go (real impl in Task 13;
//     M2 stub logs the result).
type Worker struct {
	cfg    WorkerConfig
	client *http.Client
}

// NewWorker constructs a Worker; sets sensible defaults.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 60 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Worker{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

// Handle dispatches a job to the right routine. Wire it to Queue.Consume.
func (w *Worker) Handle(ctx context.Context, j Job) error {
	switch j.Type {
	case JobCompress:
		return w.handleCompress(ctx, j)
	case JobConsolidate:
		// Task 22 fills this in.
		w.cfg.Logger.Info("worker.consolidate.skip", "job_id", j.ID, "reason", "Task 22")
		return nil
	default:
		return fmt.Errorf("worker: unknown job type %q", j.Type)
	}
}

func (w *Worker) handleCompress(ctx context.Context, j Job) error {
	var p CompressPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("decode CompressPayload: %w", err)
	}
	log := w.cfg.Logger.With("job_id", j.ID, "observation_id", p.ObservationID, "session_id", p.SessionID)
	log.Info("worker.compress.start", "attempt", j.Attempt)

	// In M2 we don't yet fetch the raw observation from Go — Task 13 will
	// extend this with a real Python round-trip when the synthetic worker can
	// pull the observation back from the state layer. For now we hit /health
	// to validate the Python service path is reachable, then log success.
	if err := w.pingPython(ctx); err != nil {
		log.Warn("worker.compress.python_unreachable", "err", err)
		return err
	}
	log.Info("worker.compress.done_stub", "note", "Task 13 wires the real round-trip")
	return nil
}

func (w *Worker) pingPython(ctx context.Context) error {
	u, err := url.Parse(w.cfg.PythonServiceURL)
	if err != nil {
		return err
	}
	u.Path = "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("python health: %d", resp.StatusCode)
	}
	return nil
}

// PostCompressed is the helper Task 13 uses to write the compressed result
// back to Go. Exposed here so the worker package owns its inter-service
// HTTP. Currently unused but kept for the Task 13 wiring.
func (w *Worker) PostCompressed(ctx context.Context, observationID string, body any) error {
	u, err := url.Parse(w.cfg.InternalCallbackURL)
	if err != nil {
		return err
	}
	u.Path = fmt.Sprintf("/internal/observation/%s/compressed", url.PathEscape(observationID))
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("post compressed: status %d", resp.StatusCode)
	}
	return nil
}
