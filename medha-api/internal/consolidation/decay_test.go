package consolidation

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/udai-kiran/medha/internal/state"
	"github.com/udai-kiran/medha/internal/testutil"
)

func openDecayStore(t *testing.T) *state.Store {
	return testutil.OpenStore(t)
}

func TestDecay_FreshMemoryBarelyChanges(t *testing.T) {
	store := openDecayStore(t)
	cfg := DefaultDecayConfig()
	e := NewDecayEngine(store, cfg, nil)
	ctx := context.Background()

	// 1-day-old memory, full strength.
	_ = store.InsertMemory(ctx, &state.MemoryRow{
		ID: "mem-1", Project: "p", Type: "fact", Tier: "semantic",
		Title: "x", Strength: 1.0, CreatedAt: time.Now().UTC().Add(-24 * time.Hour),
	})

	report, err := e.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Evicted != 0 || report.UpdatedStrength != 1 {
		t.Errorf("report = %+v", report)
	}
	got, _ := store.GetMemory(ctx, "mem-1")
	// 1.0 * 0.95 = 0.95 — well above 0.1 threshold.
	if math.Abs(got.Strength-0.95) > 0.01 {
		t.Errorf("strength = %v, want ~0.95", got.Strength)
	}
}

func TestDecay_AncientMemoryEvicted(t *testing.T) {
	store := openDecayStore(t)
	e := NewDecayEngine(store, DefaultDecayConfig(), nil)
	ctx := context.Background()

	// 90-day-old memory at strength 0.9 → 0.9 * 0.95^90 ≈ 0.0094 → evicted.
	_ = store.InsertMemory(ctx, &state.MemoryRow{
		ID: "mem-old", Project: "p", Type: "fact", Tier: "semantic",
		Title: "old", Strength: 0.9, CreatedAt: time.Now().UTC().Add(-90 * 24 * time.Hour),
	})

	report, err := e.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Evicted != 1 {
		t.Errorf("evicted = %d, want 1", report.Evicted)
	}
	if _, err := store.GetMemory(ctx, "mem-old"); err != state.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDecay_RetrievalReinforces(t *testing.T) {
	store := openDecayStore(t)
	cfg := DefaultDecayConfig()
	e := NewDecayEngine(store, cfg, nil)
	ctx := context.Background()

	created := time.Now().UTC().Add(-30 * 24 * time.Hour)
	_ = store.InsertMemory(ctx, &state.MemoryRow{
		ID: "mem-1", Project: "p", Type: "fact", Tier: "semantic",
		Title: "x", Strength: 0.5, CreatedAt: created,
	})

	// Without reinforcement: 0.5 * 0.95^30 ≈ 0.107 — close to threshold.
	_, _ = e.Run(ctx)
	withoutReinforcement, _ := store.GetMemory(ctx, "mem-1")

	// Reset for second scenario.
	_ = store.DeleteMemory(ctx, "mem-1")
	_ = store.InsertMemory(ctx, &state.MemoryRow{
		ID: "mem-1", Project: "p", Type: "fact", Tier: "semantic",
		Title: "x", Strength: 0.5, CreatedAt: created,
	})
	// Mark retrieved recently — should reset the decay baseline to now, with bonus.
	_ = store.MarkRetrieved(ctx, []string{"mem-1"})
	_, _ = e.Run(ctx)
	withReinforcement, _ := store.GetMemory(ctx, "mem-1")

	if withoutReinforcement != nil && withReinforcement.Strength <= withoutReinforcement.Strength {
		t.Errorf("retrieval should reinforce: without=%v with=%v",
			withoutReinforcement.Strength, withReinforcement.Strength)
	}
}

func TestDecay_TickerScheduler(t *testing.T) {
	// Verify the scheduler can be constructed and Stopped without leaking.
	store := openDecayStore(t)
	e := NewDecayEngine(store, DefaultDecayConfig(), nil)
	s := NewScheduler(e, time.Hour, nil)
	if s.Interval != time.Hour {
		t.Errorf("interval = %v, want 1h", s.Interval)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Start(ctx)
	cancel()
}
