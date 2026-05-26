package consolidation

import "context"

// Enqueuer satisfies api.CompressionEnqueuer using whatever Queue impl is
// configured. It is the thin glue between the capture path and the queue
// substrate so the API layer doesn't depend on the consolidation package's
// Job/Payload types.
type Enqueuer struct {
	Q Queue
}

// NewEnqueuer wires a Queue into the api.CompressionEnqueuer surface.
func NewEnqueuer(q Queue) *Enqueuer { return &Enqueuer{Q: q} }

// EnqueueCompress publishes a JobCompress.
func (e *Enqueuer) EnqueueCompress(ctx context.Context, observationID, sessionID string) error {
	if e == nil || e.Q == nil {
		return nil
	}
	job, err := NewCompressJob(observationID, sessionID)
	if err != nil {
		return err
	}
	return e.Q.Publish(ctx, job)
}

// EnqueueConsolidate publishes a JobConsolidate.
func (e *Enqueuer) EnqueueConsolidate(ctx context.Context, sessionID string, force bool) error {
	if e == nil || e.Q == nil {
		return nil
	}
	job, err := NewConsolidateJob(sessionID, force)
	if err != nil {
		return err
	}
	return e.Q.Publish(ctx, job)
}
