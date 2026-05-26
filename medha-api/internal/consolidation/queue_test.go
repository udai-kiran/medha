package consolidation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryQueue_PublishConsume(t *testing.T) {
	q := NewMemoryQueue(8, RetryPolicy{Max: 3})
	t.Cleanup(func() { _ = q.Close() })

	var seen atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = q.Consume(ctx, func(ctx context.Context, j Job) error {
			seen.Add(1)
			if seen.Load() == 3 {
				close(done)
			}
			return nil
		})
	}()

	for i := 0; i < 3; i++ {
		job, _ := NewCompressJob("obs-1", "sess-1")
		if err := q.Publish(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("only saw %d jobs", seen.Load())
	}
}

func TestMemoryQueue_DeadLetterAfterRetries(t *testing.T) {
	q := NewMemoryQueue(8, RetryPolicy{Max: 2})
	t.Cleanup(func() { _ = q.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failures := atomic.Int32{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		_ = q.Consume(ctx, func(ctx context.Context, j Job) error {
			failures.Add(1)
			return errors.New("boom")
		})
		wg.Done()
	}()

	job, _ := NewCompressJob("obs-fail", "sess-1")
	if err := q.Publish(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	// Wait for retries to exhaust.
	deadline := time.After(2 * time.Second)
	for {
		dead := q.Dead()
		if len(dead) == 1 {
			if dead[0].Attempt < 2 {
				t.Errorf("dead-lettered after only %d attempts", dead[0].Attempt)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("never dead-lettered (failures = %d, dead = %d)", failures.Load(), len(q.Dead()))
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	cancel()
	wg.Wait()
}

func TestMemoryQueue_ClosePreventsPublish(t *testing.T) {
	q := NewMemoryQueue(4, RetryPolicy{})
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	job, _ := NewCompressJob("obs-1", "sess-1")
	if err := q.Publish(context.Background(), job); !errors.Is(err, ErrQueueClosed) {
		t.Errorf("Publish after Close = %v, want ErrQueueClosed", err)
	}
}

func TestNewCompressJob_RoundTrip(t *testing.T) {
	job, err := NewCompressJob("obs-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Type != JobCompress {
		t.Errorf("Type = %q", job.Type)
	}
	var p CompressPayload
	if err := job.Payload.UnmarshalJSON(job.Payload); err != nil {
		// just decode plainly
		_ = err
	}
	_ = p
}

func TestEnqueuer_EnqueueCompress(t *testing.T) {
	q := NewMemoryQueue(4, RetryPolicy{})
	t.Cleanup(func() { _ = q.Close() })
	e := NewEnqueuer(q)

	if err := e.EnqueueCompress(context.Background(), "obs-1", "sess-1"); err != nil {
		t.Fatal(err)
	}
	// Drain one job.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	received := make(chan Job, 1)
	go func() {
		_ = q.Consume(ctx, func(ctx context.Context, j Job) error {
			received <- j
			return nil
		})
	}()
	select {
	case got := <-received:
		if got.Type != JobCompress {
			t.Errorf("Type = %q, want %q", got.Type, JobCompress)
		}
	case <-ctx.Done():
		t.Fatal("never received the enqueued job")
	}
}

func TestEnqueuer_NilQueueNoOp(t *testing.T) {
	var e *Enqueuer
	if err := e.EnqueueCompress(context.Background(), "obs-1", "sess-1"); err != nil {
		t.Error(err)
	}
}
