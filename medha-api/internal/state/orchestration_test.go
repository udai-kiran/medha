package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestActions_CRUDAndFrontier(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	for _, a := range []*ActionRow{
		{ID: "a-1", Project: "p", Title: "Plan",     Status: "completed"},
		{ID: "a-2", Project: "p", Title: "Implement", Status: "pending", Dependencies: []string{"a-1"}},
		{ID: "a-3", Project: "p", Title: "Test",      Status: "pending", Dependencies: []string{"a-2"}},
	} {
		if err := s.PutAction(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GetAction(ctx, "p", "a-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Implement" {
		t.Errorf("title = %q", got.Title)
	}

	frontier, err := s.Frontier(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(frontier) != 1 || frontier[0].ID != "a-2" {
		t.Errorf("frontier = %+v", frontier)
	}
}

func TestLease_AcquireAndConflict(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_ = s.PutAction(ctx, &ActionRow{ID: "a-1", Project: "p", Title: "x"})

	lease, err := s.AcquireLease(ctx, "p", "a-1", "alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease.HolderID != "alice" {
		t.Errorf("holder = %q", lease.HolderID)
	}

	if _, err := s.AcquireLease(ctx, "p", "a-1", "bob", time.Minute); !errors.Is(err, ErrLeaseHeld) {
		t.Errorf("conflict err = %v, want ErrLeaseHeld", err)
	}
	// Same holder can re-acquire (lease renewal).
	if _, err := s.AcquireLease(ctx, "p", "a-1", "alice", time.Hour); err != nil {
		t.Errorf("renewal: %v", err)
	}
}

func TestLease_Release(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.AcquireLease(ctx, "p", "a-1", "alice", time.Minute)
	if err := s.ReleaseLease(ctx, "p", "a-1", "alice"); err != nil {
		t.Fatal(err)
	}
	// After release, bob can take it.
	if _, err := s.AcquireLease(ctx, "p", "a-1", "bob", time.Minute); err != nil {
		t.Errorf("after release: %v", err)
	}
	// Non-holder cannot release.
	err := s.ReleaseLease(ctx, "p", "a-1", "alice")
	if err == nil {
		t.Error("expected not-holder error")
	}
}

func TestSignal_SendAndList(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, body := range []string{"hello", "you up?"} {
		err := s.SendSignal(ctx, &SignalRow{
			ID: SignalID(), Project: "p", From: "alice", To: "bob",
			Subject: "ping", Body: body,
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Microsecond) // keep ids distinct
	}
	inbox, err := s.ListInbox(ctx, "p", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 2 {
		t.Errorf("inbox len = %d, want 2", len(inbox))
	}
}

func TestRoutine_PutAndList(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, r := range []*RoutineRow{
		{ID: "r-1", Project: "p", Name: "Run tests", Steps: []string{"go test ./..."}},
		{ID: "r-2", Project: "p", Name: "Build",     Steps: []string{"go build ./..."}},
	} {
		if err := s.PutRoutine(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.ListRoutines(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Errorf("routines len = %d", len(out))
	}
}

func TestScrubLeases_RemovesExpired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, _ = s.AcquireLease(ctx, "p", "a-1", "alice", time.Nanosecond)
	time.Sleep(time.Millisecond)
	n, err := s.ScrubLeases(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("scrubbed = %d, want 1", n)
	}
}
