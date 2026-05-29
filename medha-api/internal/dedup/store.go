package dedup

import (
	"context"
	"sync"
	"time"
)

// Deduper is the narrow interface the capture path (Task 8) consumes. A
// missing impl ("no dedup") is the failure-open fallback the task notes
// allow because a missed dedup is not a security risk.
type Deduper interface {
	// Seen reports whether the (sessionID, key) tuple has been recorded within
	// the rolling window. A side effect of Seen is recording the key for
	// future calls — there is no separate Mark step, so the hot path stays
	// single-shot.
	Seen(ctx context.Context, sessionID, key string) (bool, error)
}

// Window is an in-memory, per-session rolling dedup buffer.
//
// Implementation: one map per session, holding hash → first-seen time. A
// background sweep evicts entries older than the configured duration so
// memory cannot grow unboundedly.
type Window struct {
	mu          sync.Mutex
	sessions    map[string]map[string]time.Time
	duration    time.Duration
	now         func() time.Time

	// sweep policy: when the per-session map grows past sweepThreshold,
	// evict expired entries inline as a cheap rate-limited GC.
	sweepThreshold int
}

// NewWindow returns a Window with the given retention duration (default 5 min
// per FR-3).
func NewWindow(duration time.Duration) *Window {
	if duration <= 0 {
		duration = 5 * time.Minute
	}
	return &Window{
		sessions:       make(map[string]map[string]time.Time),
		duration:       duration,
		now:            time.Now,
		sweepThreshold: 256,
	}
}

// Seen checks the per-session map; records the key when not seen. Returns
// true if (sessionID, key) was already present and not expired.
func (w *Window) Seen(_ context.Context, sessionID, key string) (bool, error) {
	if sessionID == "" || key == "" {
		return false, nil
	}

	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()

	bucket, ok := w.sessions[sessionID]
	if !ok {
		bucket = make(map[string]time.Time)
		w.sessions[sessionID] = bucket
	}

	if seenAt, present := bucket[key]; present {
		if now.Sub(seenAt) < w.duration {
			return true, nil
		}
		// Expired entry — fall through to re-record.
	}

	bucket[key] = now

	// Opportunistic eviction so memory stays bounded.
	if len(bucket) > w.sweepThreshold {
		w.evictExpiredLocked(sessionID, bucket, now)
	}

	return false, nil
}

// Sweep evicts every expired entry across every session. Intended for
// periodic background invocation; safe to call concurrently with Seen.
func (w *Window) Sweep() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	evicted := 0
	for sid, bucket := range w.sessions {
		evicted += w.evictExpiredLocked(sid, bucket, now)
		if len(bucket) == 0 {
			delete(w.sessions, sid)
		}
	}
	return evicted
}

func (w *Window) evictExpiredLocked(sid string, bucket map[string]time.Time, now time.Time) int {
	n := 0
	for k, t := range bucket {
		if now.Sub(t) >= w.duration {
			delete(bucket, k)
			n++
		}
	}
	if len(bucket) == 0 {
		delete(w.sessions, sid)
	}
	return n
}

// Size returns the total number of cached entries (for tests / metrics).
func (w *Window) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, b := range w.sessions {
		n += len(b)
	}
	return n
}

// NoOpDeduper is the failure-open fallback: it never reports a duplicate.
// Used when dedup is disabled or unconfigured (the task notes flag this as
// acceptable since a missed duplicate is not a security risk).
type NoOpDeduper struct{}

// Seen always returns false (never a duplicate).
func (NoOpDeduper) Seen(_ context.Context, sessionID, key string) (bool, error) {
	return false, nil
}
