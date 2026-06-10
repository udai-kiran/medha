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

	// PostgreSQL
	PostgresHost     string
	PostgresPort     int
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string
	PostgresSSLMode  string

	// Neo4j (optional — see ADR-0003)
	Neo4jEnabled  bool
	Neo4jURI      string
	Neo4jUsername string
	Neo4jPassword string
	Neo4jDatabase string

	// Async queue — see ADR-0001
	QueueBackend string // rabbitmq | memory
	RabbitMQURL  string

	// Python sidecar
	PythonServiceURL           string
	EmbeddingRequestTimeoutSec int
	HybridVectorTimeoutSec     int

	// Feature flags
	AgentMemoryAutoCompress  bool
	AgentMemorySlots         bool
	AgentMemoryReflect       bool
	AgentMemoryInjectContext bool
	ConsolidationEnabled     bool
	LessonDecayEnabled       bool

	// AbbreviationExpansionEnabled gates the per-project abbreviation glossary:
	// the worker ships the project's known abbreviations to Python /compress for
	// inline expansion, and the compression callback merges back any the LLM
	// newly detects. When false, neither the fetch/send nor the merge happens.
	AbbreviationExpansionEnabled bool

	// Decay tuning — see ADR-0002
	DecayRatePerDay        float64
	DecayEvictionThreshold float64
	DecayReviewLow         float64
	DecayReviewHigh        float64

	// TSQuery expansion — LLM compiles natural-language to structured ts_query
	TSQueryExpandEnabled bool

	// Reranking (cross-encoder via Python /rerank → Bifrost → Cohere)
	RerankEnabled  bool
	RerankPoolSize int // candidates fed to the reranker; 0 → 30 default
	RerankTopK     int // reranker output size; 0 → use request limit

	// Recency weighting — boosts recently-indexed memories/observations after
	// RRF fusion so newer sessions outrank older ones (Generative-Agents-style
	// recency term; see ADR notes). Weight 0 disables (preserves pure relevance).
	SearchRecencyWeight       float64 // boost magnitude; 0 disables. Multiplier = 1 + w·exp(-age/halfLife)
	SearchRecencyHalfLifeDays float64 // age (days) at which the recency bonus halves

	// Cosine dedup: applied async in the compression worker before vector-indexing.
	// Near-duplicate observations (cosine sim ≥ threshold within the window) are
	// skipped. 0 disables. When EMBEDDING_MODEL is unset the embedder uses local
	// hashing, making cosine comparisons semantically meaningless — leave threshold
	// at 0 unless a real embedding model is configured via EMBEDDING_MODEL.
	DedupeCosineSimilarityThreshold float64
	DedupeCosineSimilarityWindowSec int // seconds; 0 → 300 default

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
		Port:                         getInt("PORT", 3111),
		ViewerPort:                   getInt("VIEWER_PORT", 3113),
		LogLevel:                     getString("LOG_LEVEL", "info"),
		PostgresHost:                 getString("POSTGRES_HOST", "localhost"),
		PostgresPort:                 getInt("POSTGRES_PORT", 5432),
		PostgresUser:                 getString("POSTGRES_USER", "medha"),
		PostgresPassword:             getString("POSTGRES_PASSWORD", ""),
		PostgresDB:                   getString("POSTGRES_DB", "medha"),
		PostgresSSLMode:              getString("POSTGRES_SSLMODE", "disable"),
		Neo4jEnabled:                 getBool("NEO4J_ENABLED", false),
		Neo4jURI:                     getString("NEO4J_URI", "bolt://localhost:7687"),
		Neo4jUsername:                getString("NEO4J_USERNAME", "neo4j"),
		Neo4jPassword:                getString("NEO4J_PASSWORD", ""),
		Neo4jDatabase:                getString("NEO4J_DATABASE", "medha"),
		QueueBackend:                 getString("QUEUE_BACKEND", "memory"),
		RabbitMQURL:                  getString("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		PythonServiceURL:             getString("PYTHON_SERVICE_URL", "http://localhost:5000"),
		EmbeddingRequestTimeoutSec:   getInt("EMBEDDING_REQUEST_TIMEOUT_SEC", 5),
		HybridVectorTimeoutSec:       getInt("HYBRID_VECTOR_TIMEOUT_SEC", 2),
		AgentMemoryAutoCompress:      getBool("AGENTMEMORY_AUTO_COMPRESS", false),
		AgentMemorySlots:             getBool("AGENTMEMORY_SLOTS", false),
		AgentMemoryReflect:           getBool("AGENTMEMORY_REFLECT", false),
		AgentMemoryInjectContext:     getBool("AGENTMEMORY_INJECT_CONTEXT", false),
		ConsolidationEnabled:         getBool("CONSOLIDATION_ENABLED", true),
		LessonDecayEnabled:           getBool("LESSON_DECAY_ENABLED", true),
		AbbreviationExpansionEnabled: getBool("ABBREVIATION_EXPANSION_ENABLED", true),
		DecayRatePerDay:              getFloat("DECAY_RATE_PER_DAY", 0.95),
		DecayEvictionThreshold:       getFloat("DECAY_EVICTION_THRESHOLD", 0.10),
		DecayReviewLow:               getFloat("DECAY_REVIEW_LOW", 0.10),
		DecayReviewHigh:              getFloat("DECAY_REVIEW_HIGH", 0.30),
		TSQueryExpandEnabled:         getBool("TSQUERY_EXPAND_ENABLED", false),
		RerankEnabled:                getBool("RERANK_ENABLED", false),
		RerankPoolSize:               getInt("RERANK_POOL_SIZE", 30),
		RerankTopK:                   getInt("RERANK_TOP_K", 0),
		SearchRecencyWeight:          getFloat("SEARCH_RECENCY_WEIGHT", 0.3),
		SearchRecencyHalfLifeDays:    getFloat("SEARCH_RECENCY_HALFLIFE_DAYS", 7.0),
		DedupeCosineSimilarityThreshold: getFloat("DEDUP_COSINE_THRESHOLD", 0),
		DedupeCosineSimilarityWindowSec: getInt("DEDUP_COSINE_WINDOW_SEC", 300),
		AgentMemorySecret:               getString("AGENTMEMORY_SECRET", ""),
		OTELExporterEndpoint:         getString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTELServiceName:              getString("OTEL_SERVICE_NAME", "agent-mem-go"),
		ShutdownTimeout:              time.Duration(getInt("SHUTDOWN_TIMEOUT_SEC", 15)) * time.Second,
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
