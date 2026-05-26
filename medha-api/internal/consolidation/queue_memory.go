package consolidation

import (
	"context"
	"errors"
	"sync"
	"time"
)

// MemoryQueue is the in-memory Queue implementation used by tests and the
// lightweight deployment profile (ADR-0001). It is *not* durable across
// process restarts; production deployments use the RabbitMQ backend.
type MemoryQueue struct {
	mu       sync.Mutex
	ch       chan Job
	dead     []Job
	policy   RetryPolicy
	closed   bool
	consumed sync.WaitGroup
}

// NewMemoryQueue returns a Queue backed by a buffered channel. bufSize=0
// gives a synchronous handoff; tests typically pass 64 for ergonomic
// Publish without an active consumer.
func NewMemoryQueue(bufSize int, policy RetryPolicy) *MemoryQueue {
	if bufSize <= 0 {
		bufSize = 64
	}
	if policy.Max == 0 {
		policy.Max = 3
	}
	return &MemoryQueue{
		ch:     make(chan Job, bufSize),
		policy: policy,
	}
}

// Publish enqueues a job. Returns ErrClosed if the queue is shut down.
func (q *MemoryQueue) Publish(ctx context.Context, j Job) error {
	if !j.Type.IsValid() {
		return errors.New("MemoryQueue.Publish: invalid job type")
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrQueueClosed
	}
	ch := q.ch
	q.mu.Unlock()

	select {
	case ch <- j:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Consume blocks until ctx is cancelled, invoking h for every job. Failures
// retry with exponential backoff; after policy.Max attempts the job is
// dead-lettered into an internal list (Dead).
func (q *MemoryQueue) Consume(ctx context.Context, h Handler) error {
	if h == nil {
		return errors.New("MemoryQueue.Consume: nil handler")
	}
	q.mu.Lock()
	ch := q.ch
	q.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case j, ok := <-ch:
			if !ok {
				return nil
			}
			q.consumed.Add(1)
			q.handleOne(ctx, j, h)
			q.consumed.Done()
		}
	}
}

func (q *MemoryQueue) handleOne(ctx context.Context, j Job, h Handler) {
	delay := 100 * time.Millisecond
	for {
		err := h(ctx, j)
		if err == nil {
			return
		}
		j.Attempt++
		if j.Attempt >= q.policy.Max {
			q.mu.Lock()
			q.dead = append(q.dead, j)
			q.mu.Unlock()
			return
		}
		// Backoff with jitter omitted for determinism in tests.
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
	}
}

// Dead returns a copy of dead-lettered jobs (for tests / diagnostics).
func (q *MemoryQueue) Dead() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, len(q.dead))
	copy(out, q.dead)
	return out
}

// Close shuts the queue down. After Close, Publish returns ErrQueueClosed
// and Consume drains pending jobs then returns.
func (q *MemoryQueue) Close() error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	close(q.ch)
	q.mu.Unlock()
	q.consumed.Wait()
	return nil
}

// ErrQueueClosed is returned by Publish after Close.
var ErrQueueClosed = errors.New("queue closed")
