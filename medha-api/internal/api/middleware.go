package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/udai-kiran/medha/internal/telemetry"
)

// RequestIDHeader is the HTTP header carrying the request ID across services.
const RequestIDHeader = "X-Request-ID"

// requestID attaches an ID to every request (using the inbound value if present)
// and exposes it via the response header.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := r.Context()
		next.ServeHTTP(w, r.WithContext(ctx))
		_ = id // logger attaches it via withLogger
	})
}

// withLogger puts a request-scoped logger on the context (carrying request ID).
func withLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := telemetry.LoggerFrom(r.Context())
		l := base.With(
			"request_id", w.Header().Get(RequestIDHeader),
			"method", r.Method,
			"path", r.URL.Path,
		)
		ctx := telemetry.WithLogger(r.Context(), l)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestLog records method, path, status, and duration once the handler returns.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		telemetry.LoggerFrom(r.Context()).Info("http.request",
			"status", ww.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// recoverer catches panics and converts them to a 500 JSON response so a single
// buggy handler never takes the process down.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				telemetry.LoggerFrom(r.Context()).Error("http.panic",
					"err", rec,
					"stack", string(debug.Stack()),
				)
				WriteError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors emits permissive CORS headers; tighten in Task 33.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder lets requestLog read the status the handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
