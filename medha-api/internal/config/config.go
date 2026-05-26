// Package config loads and validates runtime configuration from environment
// variables. All later tasks read their tunables from the returned Config —
// keep this the single source of truth.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the typed view of every environment variable the Go service reads.
// Defaults match .env.example at the repo root.
type Config struct {
	// HTTP servers
	Port       int
	ViewerPort int
	LogLevel   string

	// State
	SQLitePath string

	// Neo4j (optional — see ADR-0003)
	Neo4jEnabled  bool
	Neo4jURI      string
	Neo4jUsername string
	Neo4jPassword string

	// Async queue — see ADR-0001
	QueueBackend string // rabbitmq | memory
	RabbitMQURL  string

	// Python sidecar
	PythonServiceURL string

	// Feature flags
	AgentMemoryAutoCompress bool
	AgentMemorySlots        bool
	AgentMemoryReflect      bool
	ConsolidationEnabled    bool
	LessonDecayEnabled      bool

	// Decay tuning — see ADR-0002
	DecayRatePerDay         float64
	DecayEvictionThreshold  float64
	DecayReviewLow          float64
	DecayReviewHigh         float64

	// Security
	AgentMemorySecret string

	// Observability
	OTELExporterEndpoint string
	OTELServiceName      string

	// Derived
	ShutdownTimeout time.Duration
}

// FromEnv reads every variable; missing optional values fall back to defaults.
// Validation is split out (Validate) so callers can format errors uniformly.
func FromEnv() *Config {
	return &Config{
		Port:                    getInt("PORT", 3111),
		ViewerPort:              getInt("VIEWER_PORT", 3113),
		LogLevel:                getString("LOG_LEVEL", "info"),
		SQLitePath:              getString("SQLITE_PATH", "./data/agentmemory.db"),
		Neo4jEnabled:            getBool("NEO4J_ENABLED", false),
		Neo4jURI:                getString("NEO4J_URI", "bolt://localhost:7687"),
		Neo4jUsername:           getString("NEO4J_USERNAME", "neo4j"),
		Neo4jPassword:           getString("NEO4J_PASSWORD", ""),
		QueueBackend:            getString("QUEUE_BACKEND", "memory"),
		RabbitMQURL:             getString("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		PythonServiceURL:        getString("PYTHON_SERVICE_URL", "http://localhost:5000"),
		AgentMemoryAutoCompress: getBool("AGENTMEMORY_AUTO_COMPRESS", false),
		AgentMemorySlots:        getBool("AGENTMEMORY_SLOTS", false),
		AgentMemoryReflect:      getBool("AGENTMEMORY_REFLECT", false),
		ConsolidationEnabled:    getBool("CONSOLIDATION_ENABLED", true),
		LessonDecayEnabled:      getBool("LESSON_DECAY_ENABLED", true),
		DecayRatePerDay:         getFloat("DECAY_RATE_PER_DAY", 0.95),
		DecayEvictionThreshold:  getFloat("DECAY_EVICTION_THRESHOLD", 0.10),
		DecayReviewLow:          getFloat("DECAY_REVIEW_LOW", 0.10),
		DecayReviewHigh:         getFloat("DECAY_REVIEW_HIGH", 0.30),
		AgentMemorySecret:       getString("AGENTMEMORY_SECRET", ""),
		OTELExporterEndpoint:    getString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTELServiceName:         getString("OTEL_SERVICE_NAME", "agent-mem-go"),
		ShutdownTimeout:         time.Duration(getInt("SHUTDOWN_TIMEOUT_SEC", 15)) * time.Second,
	}
}

func getString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getFloat(key string, def float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	case "0", "f", "false", "no", "n", "off":
		return false
	}
	return def
}

// Addr returns the API listen address (e.g. ":3111").
func (c *Config) Addr() string { return fmt.Sprintf(":%d", c.Port) }

// ViewerAddr returns the viewer listen address (e.g. ":3113").
func (c *Config) ViewerAddr() string { return fmt.Sprintf(":%d", c.ViewerPort) }
