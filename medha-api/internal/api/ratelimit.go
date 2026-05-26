package api

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a simple per-key token bucket: each key gets `rate` tokens
// per `window`. Keys are typically the bearer token (when auth is on) or the
// remote IP (when it isn't).
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int           // tokens per window
	window   time.Duration // refill interval
}

type bucket struct {
	tokens     int
	lastRefill time.Time
}

// NewRateLimiter returns a limiter allowing `rate` requests per `window`.
// rate=0 disables limiting (returns a passthrough middleware).
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	if rate <= 0 {
		return nil
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    rate,
		window:  window,
	}
}

// Middleware returns a chi-compatible middleware. nil receiver is a no-op so
// callers can wire `rl.Middleware()` unconditionally.
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	if rl == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rl.key(r)
			if !rl.allow(key) {
				WriteError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// key derives a per-client identifier. Token-based when Authorization header
// is present, otherwise the remote IP.
func (rl *RateLimiter) key(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		return h
	}
	// X-Forwarded-For (first hop) > RemoteAddr.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := indexByte(xff, ','); comma > 0 {
			return xff[:comma]
		}
		return xff
	}
	return r.RemoteAddr
}

// allow consumes a token. Refill on demand to keep the data structure tiny.
func (rl *RateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.rate, lastRefill: now}
		rl.buckets[key] = b
	}
	// Refill: one full bucket per window.
	elapsed := now.Sub(b.lastRefill)
	if elapsed >= rl.window {
		b.tokens = rl.rate
		b.lastRefill = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
