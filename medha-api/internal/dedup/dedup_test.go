package dedup

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestComputeKey_StableUnderKeyOrdering(t *testing.T) {
	// Same logical input expressed with different key ordering / whitespace
	// must produce identical hashes (NFR-8: ≥99% dedup accuracy on shuffled
	// key ordering).
	a := json.RawMessage(`{"file_path":"/x.go","limit":100}`)
	b := json.RawMessage(`{"limit":100,"file_path":"/x.go"}`)
	c := json.RawMessage(`{
        "file_path": "/x.go",
        "limit": 100
    }`)

	ka, err := ComputeKey("sess-1", "read", a)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := ComputeKey("sess-1", "read", b)
	if err != nil {
		t.Fatal(err)
	}
	kc, err := ComputeKey("sess-1", "read", c)
	if err != nil {
		t.Fatal(err)
	}

	if ka != kb || ka != kc {
		t.Errorf("hashes diverged under reordering: %s / %s / %s", ka, kb, kc)
	}
}

func TestComputeKey_DistinguishesByToolName(t *testing.T) {
	in := json.RawMessage(`{"x":1}`)
	k1, _ := ComputeKey("sess-1", "read", in)
	k2, _ := ComputeKey("sess-1", "write", in)
	if k1 == k2 {
		t.Error("different toolName should hash differently")
	}
}

func TestComputeKey_DistinguishesBySession(t *testing.T) {
	in := json.RawMessage(`{"x":1}`)
	k1, _ := ComputeKey("sess-1", "read", in)
	k2, _ := ComputeKey("sess-2", "read", in)
	if k1 == k2 {
		t.Error("different session should hash differently")
	}
}

func TestComputeKey_NilToolInput(t *testing.T) {
	k1, err := ComputeKey("sess-1", "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := ComputeKey("sess-1", "read", nil)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Error("nil input should hash deterministically")
	}
}

func TestComputeKey_NestedCanonicalisation(t *testing.T) {
	a := json.RawMessage(`{"o":{"b":2,"a":1},"arr":[3,1,2]}`)
	b := json.RawMessage(`{"arr":[3,1,2],"o":{"a":1,"b":2}}`)
	ka, _ := ComputeKey("s", "t", a)
	kb, _ := ComputeKey("s", "t", b)
	if ka != kb {
		t.Errorf("nested objects not canonicalised: %s vs %s", ka, kb)
	}
}

func TestWindow_DuplicateWithinWindow(t *testing.T) {
	w := NewWindow(time.Minute)
	ctx := context.Background()

	dup, err := w.Seen(ctx, "sess-1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if dup {
		t.Error("first Seen should be false")
	}
	dup, _ = w.Seen(ctx, "sess-1", "k1")
	if !dup {
		t.Error("second Seen within window should be true")
	}
}

func TestWindow_DifferentSessionsIsolated(t *testing.T) {
	w := NewWindow(time.Minute)
	ctx := context.Background()
	_, _ = w.Seen(ctx, "sess-1", "k1")
	dup, _ := w.Seen(ctx, "sess-2", "k1")
	if dup {
		t.Error("same key, different session must not be a duplicate")
	}
}

func TestWindow_ExpiryFreshes(t *testing.T) {
	w := NewWindow(time.Minute)
	current := time.Now()
	w.now = func() time.Time { return current }

	ctx := context.Background()
	_, _ = w.Seen(ctx, "sess-1", "k1")

	// Advance past expiry.
	current = current.Add(2 * time.Minute)
	dup, _ := w.Seen(ctx, "sess-1", "k1")
	if dup {
		t.Error("after window expiry, same key should be fresh")
	}
}

func TestWindow_SweepBounded(t *testing.T) {
	w := NewWindow(50 * time.Millisecond)
	w.sweepThreshold = 4
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		key := string(rune('a'+i%26)) + "-" + string(rune('0'+i%10)) + "-" + string(rune('a'+(i*7)%26))
		_, _ = w.Seen(ctx, "sess-1", key+"-"+keySuffix(i))
	}
	// Sleep past expiry, sweep, expect bucket pruned.
	time.Sleep(60 * time.Millisecond)
	evicted := w.Sweep()
	if evicted == 0 && w.Size() > 0 {
		// Either evicted via inline sweep already, or the size is small now —
		// what we really care about is that we didn't keep 1000 entries forever.
		if w.Size() > 100 {
			t.Errorf("expected bounded growth, got Size = %d", w.Size())
		}
	}
}

func TestWindow_Concurrent(t *testing.T) {
	w := NewWindow(time.Second)
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = w.Seen(ctx, "sess", keySuffix(gid*1000+i))
			}
		}(g)
	}
	wg.Wait()
	// Just asserting no panic / data races (run -race in CI).
	if w.Size() == 0 {
		t.Error("expected entries after concurrent fills")
	}
}

func TestNoOpDeduper_NeverDuplicates(t *testing.T) {
	var d Deduper = NoOpDeduper{}
	for i := 0; i < 5; i++ {
		dup, err := d.Seen(context.Background(), "sess", "k")
		if err != nil || dup {
			t.Errorf("NoOpDeduper.Seen = (%v, %v), want (false, nil)", dup, err)
		}
	}
}

func keySuffix(i int) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 4)
	for j := 0; j < 4; j++ {
		out[j] = hex[(i>>(j*4))&0xf]
	}
	return string(out)
}
