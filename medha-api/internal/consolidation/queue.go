// Package consolidation owns the async job substrate. The narrow Queue
// interface (ADR-0001) keeps the backend swappable: RabbitMQ in production,
// in-memory in dev/test, Redis-capable as a future option.
package consolidation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// JobType is the kind of async work being scheduled. Each value pairs with
// a Job<Type>Payload struct so the consumer can route by type.
type JobType string

const (
	// JobCompress is enqueued by the capture path (Task 8) once a RawObservation
	// is stored. The worker invokes Python /compress and writes the result back
	// via Go /internal/observation/{id}/compressed.
	JobCompress JobType = "compress"
	// JobConsolidate is enqueued at SessionEnd (Task 22). The worker fans out to
	// extract → cluster → memorise.
	JobConsolidate JobType = "consolidate"
)

// IsValid reports whether t is a known job type.
func (t JobType) IsValid() bool {
	return t == JobCompress || t == JobConsolidate
}

// Job is the wire envelope for everything published to the queue. Payload is
// a JSON blob whose schema depends on Type.
type Job struct {
	ID      string          `json:"id"`
	Type    JobType         `json:"type"`
	Payload json.RawMessage `json:"payload"`
	// Attempt counts retries the worker has already performed.
	Attempt int `json:"attempt,omitempty"`
}

// CompressPayload identifies which observation to compress.
type CompressPayload struct {
	ObservationID string `json:"observationId"`
	SessionID     string `json:"sessionId"`
}

// ConsolidatePayload identifies a session and whether to force re-consolidation.
type ConsolidatePayload struct {
	SessionID string `json:"sessionId"`
	Force     bool   `json:"force,omitempty"`
}

// Handler processes a single dequeued job. Returning a nil error acks the
// job; returning a non-nil error triggers retry (up to RetryPolicy.Max) and
// then dead-letters.
type Handler func(ctx context.Context, job Job) error

// Queue is the narrow interface every backend implements (ADR-0001).
//
// Publish enqueues a job; Consume blocks until ctx is cancelled, calling h
// for every dequeued job. Implementations MUST be safe for concurrent use.
type Queue interface {
	Publish(ctx context.Context, job Job) error
	Consume(ctx context.Context, h Handler) error
	Close() error
}

// RetryPolicy tunes retry + dead-letter behaviour. Zero values give a
// reasonable default (3 retries with 100ms base delay, exponential).
type RetryPolicy struct {
	Max int
}

// NewCompressJob builds a JobCompress with a stable id (the observation id is
// already unique, so we mirror it).
func NewCompressJob(observationID, sessionID string) (Job, error) {
	if observationID == "" {
		return Job{}, errors.New("NewCompressJob: observationID required")
	}
	payload, err := json.Marshal(CompressPayload{ObservationID: observationID, SessionID: sessionID})
	if err != nil {
		return Job{}, fmt.Errorf("NewCompressJob: %w", err)
	}
	return Job{ID: "job-cmp-" + observationID, Type: JobCompress, Payload: payload}, nil
}

// NewConsolidateJob builds a JobConsolidate.
func NewConsolidateJob(sessionID string, force bool) (Job, error) {
	if sessionID == "" {
		return Job{}, errors.New("NewConsolidateJob: sessionID required")
	}
	payload, err := json.Marshal(ConsolidatePayload{SessionID: sessionID, Force: force})
	if err != nil {
		return Job{}, err
	}
	return Job{ID: "job-con-" + sessionID, Type: JobConsolidate, Payload: payload}, nil
}
