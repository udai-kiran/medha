// Package telemetry holds observability primitives. For now it owns the
// structured JSON logger; Task 29 layers OTEL traces + Prometheus metrics on top.
package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// ctxKey is the unexported context key for the request-scoped logger.
type ctxKey struct{}

// NewLogger returns a slog.Logger emitting JSON to stdout at the requested level.
// Unknown levels fall back to info — Validate() should have rejected them already.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

// WithLogger attaches a logger to the context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// LoggerFrom returns the logger stored on ctx, or the default logger if none.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
