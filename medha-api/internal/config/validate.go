package config

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks that the loaded config makes sense. Failures here should
// abort startup with a clear, single-line message — there is no useful
// recovery for a misconfigured server.
func (c *Config) Validate() error {
	var errs []string

	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Sprintf("PORT out of range: %d", c.Port))
	}
	if c.ViewerPort < 1 || c.ViewerPort > 65535 {
		errs = append(errs, fmt.Sprintf("VIEWER_PORT out of range: %d", c.ViewerPort))
	}
	if c.Port == c.ViewerPort {
		errs = append(errs, "PORT and VIEWER_PORT must differ")
	}
	if c.SQLitePath == "" {
		errs = append(errs, "SQLITE_PATH is required")
	}
	if c.PythonServiceURL == "" {
		errs = append(errs, "PYTHON_SERVICE_URL is required")
	}

	switch c.QueueBackend {
	case "memory", "rabbitmq":
		// ok
	default:
		errs = append(errs, fmt.Sprintf("QUEUE_BACKEND must be 'memory' or 'rabbitmq', got %q", c.QueueBackend))
	}
	if c.QueueBackend == "rabbitmq" && c.RabbitMQURL == "" {
		errs = append(errs, "RABBITMQ_URL is required when QUEUE_BACKEND=rabbitmq")
	}

	if c.Neo4jEnabled && c.Neo4jURI == "" {
		errs = append(errs, "NEO4J_URI is required when NEO4J_ENABLED=true")
	}

	if c.DecayRatePerDay <= 0 || c.DecayRatePerDay >= 1 {
		errs = append(errs, fmt.Sprintf("DECAY_RATE_PER_DAY must be in (0,1), got %v", c.DecayRatePerDay))
	}
	if c.DecayEvictionThreshold < 0 || c.DecayEvictionThreshold > 1 {
		errs = append(errs, fmt.Sprintf("DECAY_EVICTION_THRESHOLD must be in [0,1], got %v", c.DecayEvictionThreshold))
	}

	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
		// ok
	default:
		errs = append(errs, fmt.Sprintf("LOG_LEVEL must be debug|info|warn|error, got %q", c.LogLevel))
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("invalid config: " + strings.Join(errs, "; "))
}
