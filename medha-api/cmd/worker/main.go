// Command worker is the agent_mem async job consumer.
// It pulls compress + consolidate jobs from the configured queue backend
// (RabbitMQ in prod, in-memory in dev) and drives the Python service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/udai-kiran/medha/internal/config"
	"github.com/udai-kiran/medha/internal/consolidation"
	"github.com/udai-kiran/medha/internal/telemetry"
)

func main() {
	cfg := config.FromEnv()
	logger := telemetry.NewLogger(cfg.LogLevel)
	if err := cfg.Validate(); err != nil {
		logger.Error("config.invalid", "err", err)
		os.Exit(2)
	}

	queue, err := buildQueue(cfg, logger)
	if err != nil {
		logger.Error("queue.build", "err", err)
		os.Exit(1)
	}
	defer func() { _ = queue.Close() }()

	worker := consolidation.NewWorker(consolidation.WorkerConfig{
		PythonServiceURL:    cfg.PythonServiceURL,
		InternalCallbackURL: "http://localhost" + cfg.Addr(),
		HTTPTimeout:         60 * time.Second,
		Logger:              logger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	consumeErr := make(chan error, 1)
	go func() {
		logger.Info("worker.consume.start", "backend", cfg.QueueBackend)
		consumeErr <- queue.Consume(ctx, worker.Handle)
	}()

	select {
	case <-stop:
		logger.Info("worker.shutdown.begin")
		cancel()
		<-consumeErr
		logger.Info("worker.shutdown.done")
	case err := <-consumeErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("worker.consume.failed", "err", err)
			os.Exit(1)
		}
	}
}

// buildQueue picks the backend named by cfg.QueueBackend. RabbitMQ wiring is
// stubbed in M2 — pulling in an AMQP client is the next slice of Task 12 when
// a broker is actually available in the dev loop. The in-memory backend
// keeps the worker process useful for local exercises (Task 18 onward).
func buildQueue(cfg *config.Config, logger *slog.Logger) (consolidation.Queue, error) {
	switch cfg.QueueBackend {
	case "memory":
		return consolidation.NewMemoryQueue(256, consolidation.RetryPolicy{Max: 3}), nil
	case "rabbitmq":
		logger.Warn("queue.rabbitmq.stub", "msg", "RabbitMQ backend stubbed; using in-memory for now")
		return consolidation.NewMemoryQueue(256, consolidation.RetryPolicy{Max: 3}), nil
	default:
		return nil, errors.New("unknown queue backend: " + cfg.QueueBackend)
	}
}
