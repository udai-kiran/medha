package consolidation

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/udai-kiran/medha/internal/state"
)

// DecayConfig matches the env vars defined in ADR-0002 / .env.example. The
// scheduler reads it once at startup and re-reads on SIGHUP (future work).
type DecayConfig struct {
	// RatePerDay is the multiplier applied per elapsed day (default 0.95).
	RatePerDay float64
	// EvictionThreshold is the strength at which a memory is hard-deleted.
	EvictionThreshold float64
	// WorkingTTL is the TTL for uncompressed observations (FR-27).
	WorkingTTL time.Duration
	// EpisodicTTL is the TTL for compressed observations (FR-27).
	EpisodicTTL time.Duration
	// ReinforcementOnRetrieval is added to strength each time a memory is
	// retrieved (capped at 1.0 by the engine).
	ReinforcementOnRetrieval float64
}

// DefaultDecayConfig matches the .env.example defaults.
func DefaultDecayConfig() DecayConfig {
	return DecayConfig{
		RatePerDay:               0.95,
		EvictionThreshold:        0.1,
		WorkingTTL:               24 * time.Hour,
		EpisodicTTL:              7 * 24 * time.Hour,
		ReinforcementOnRetrieval: 0.05,
	}
}

// DecayReport summarises what one decay run did.
type DecayReport struct {
	Evaluated        int   `json:"evaluated"`
	Evicted          int   `json:"evicted"`
	UpdatedStrength  int   `json:"updated_strength"`
	WorkingEvicted   int64 `json:"working_evicted"`
	EpisodicEvicted  int64 `json:"episodic_evicted"`
	DurationMillisec int64 `json:"duration_ms"`
}

// DecayEngine applies Ebbinghaus decay (`strength *= rate^days`) to every
// Semantic / Procedural memory, then enforces observation TTLs.
//
// Working / Episodic tier memories typically aren't created by the pipeline
// today (Task 22 emits Semantic), but if any exist they're skipped — those
// tiers are time-boxed rather than decayed.
type DecayEngine struct {
	Store  *state.Store
	Cfg    DecayConfig
	Logger *slog.Logger
	// Now returns the current time; tests override this.
	Now func() time.Time
}

// NewDecayEngine wires defaults.
func NewDecayEngine(s *state.Store, cfg DecayConfig, logger *slog.Logger) *DecayEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &DecayEngine{Store: s, Cfg: cfg, Logger: logger, Now: time.Now}
}

// Run applies decay across all decayable memories and enforces observation
// TTLs. Safe to call concurrently with reads; uses one transaction per memory
// to avoid blocking the API.
func (e *DecayEngine) Run(ctx context.Context) (DecayReport, error) {
	start := time.Now()
	report := DecayReport{}

	rows, err := e.Store.DB.QueryContext(ctx, `
        SELECT id, strength, created_at, last_retrieved_at, tier
        FROM memories
        WHERE tier IN ('semantic', 'procedural') AND is_latest = 1
    `)
	if err != nil {
		return report, err
	}
	defer func() { _ = rows.Close() }()

	now := e.Now().UTC()
	type pending struct {
		id       string
		strength float64
	}
	var updates []pending
	var deletes []string

	for rows.Next() {
		var (
			id              string
			strength        float64
			createdAt       string
			lastRetrieved   *string
			tier            string
		)
		var lastRetrievedNS interface{ Scan(...any) error } // dummy for clarity
		_ = lastRetrievedNS
		var lastRetrievedSQL *string
		if err := rows.Scan(&id, &strength, &createdAt, &lastRetrievedSQL, &tier); err != nil {
			return report, err
		}
		lastRetrieved = lastRetrievedSQL
		report.Evaluated++

		base, _ := time.Parse(time.RFC3339Nano, createdAt)
		if lastRetrieved != nil && *lastRetrieved != "" {
			if t, err := time.Parse(time.RFC3339Nano, *lastRetrieved); err == nil && t.After(base) {
				base = t
				// Add reinforcement bonus.
				strength = math.Min(1.0, strength+e.Cfg.ReinforcementOnRetrieval)
			}
		}
		daysOld := now.Sub(base).Hours() / 24
		if daysOld < 0 {
			daysOld = 0
		}
		newStrength := strength * math.Pow(e.Cfg.RatePerDay, daysOld)
		if newStrength < 0 {
			newStrength = 0
		}

		if newStrength < e.Cfg.EvictionThreshold {
			deletes = append(deletes, id)
			report.Evicted++
		} else {
			updates = append(updates, pending{id, newStrength})
			report.UpdatedStrength++
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}

	for _, u := range updates {
		if err := e.Store.UpdateMemoryStrength(ctx, u.id, u.strength); err != nil {
			e.Logger.Warn("decay.update_failed", "memory_id", u.id, "err", err)
		}
	}
	for _, id := range deletes {
		if err := e.Store.DeleteMemory(ctx, id); err != nil {
			e.Logger.Warn("decay.delete_failed", "memory_id", id, "err", err)
		}
	}

	w, ep, err := e.Store.EvictExpiredObservations(ctx, e.Cfg.WorkingTTL, e.Cfg.EpisodicTTL)
	if err != nil {
		return report, err
	}
	report.WorkingEvicted = w
	report.EpisodicEvicted = ep
	report.DurationMillisec = time.Since(start).Milliseconds()

	e.Logger.Info("decay.done",
		"evaluated", report.Evaluated,
		"evicted", report.Evicted,
		"updated", report.UpdatedStrength,
		"working_evicted", report.WorkingEvicted,
		"episodic_evicted", report.EpisodicEvicted,
		"duration_ms", report.DurationMillisec,
	)
	return report, nil
}

// Scheduler runs the decay engine at the configured cadence (default nightly).
type Scheduler struct {
	Engine   *DecayEngine
	Interval time.Duration
	Logger   *slog.Logger
}

// NewScheduler returns a Scheduler that runs `engine.Run` every `interval`.
// interval=0 falls back to 24h.
func NewScheduler(engine *DecayEngine, interval time.Duration, logger *slog.Logger) *Scheduler {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{Engine: engine, Interval: interval, Logger: logger}
}

// Start runs Run in a loop until ctx is cancelled. Intended to be started in
// a goroutine from main().
func (s *Scheduler) Start(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()

	// First run shortly after startup so operators get fast feedback rather
	// than waiting the whole interval.
	initial := time.NewTimer(2 * time.Minute)
	defer initial.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
			if _, err := s.Engine.Run(ctx); err != nil {
				s.Logger.Error("decay.run_failed", "err", err)
			}
		case <-t.C:
			if _, err := s.Engine.Run(ctx); err != nil {
				s.Logger.Error("decay.run_failed", "err", err)
			}
		}
	}
}
