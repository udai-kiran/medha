package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHookType_RoundTrip(t *testing.T) {
	in := HookPostToolUse
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"post_tool_use"` {
		t.Errorf("Marshal = %s", b)
	}
	var out HookType
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("round trip = %q", out)
	}
}

func TestHookType_RejectsUnknown(t *testing.T) {
	var h HookType
	err := json.Unmarshal([]byte(`"nope"`), &h)
	if err == nil || !strings.Contains(err.Error(), "unknown HookType") {
		t.Errorf("want unknown error, got %v", err)
	}
}

func TestHookPayload_Validate(t *testing.T) {
	cases := []struct {
		name    string
		p       HookPayload
		wantErr string
	}{
		{
			name: "ok",
			p: HookPayload{
				HookType: HookSessionStart, SessionID: "sess-1",
				Timestamp: time.Now(),
			},
		},
		{name: "missing session", p: HookPayload{HookType: HookSessionStart, Timestamp: time.Now()}, wantErr: "sessionId"},
		{name: "missing timestamp", p: HookPayload{HookType: HookSessionStart, SessionID: "sess-1"}, wantErr: "timestamp"},
		{name: "bad hook", p: HookPayload{HookType: "garbage", SessionID: "sess-1", Timestamp: time.Now()}, wantErr: "hookType"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want contains %q", err, c.wantErr)
			}
		})
	}
}

func TestRawObservation_DefaultsModality(t *testing.T) {
	o := RawObservation{
		ID: "obs-1", SessionID: "sess-1", HookType: HookPostToolUse,
		Timestamp: time.Now(),
	}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if o.Modality != ModalityText {
		t.Errorf("Modality = %q, want text", o.Modality)
	}
}

func TestRawObservation_BadModality(t *testing.T) {
	o := RawObservation{
		ID: "obs-1", SessionID: "sess-1", HookType: HookPostToolUse,
		Timestamp: time.Now(), Modality: Modality("audio"),
	}
	if err := o.Validate(); err == nil || !strings.Contains(err.Error(), "modality") {
		t.Errorf("err = %v, want modality error", err)
	}
}

func TestCompressedObservation_Validate(t *testing.T) {
	c := CompressedObservation{
		ID: "obs-1", SessionID: "sess-1", Type: "file_read", Title: "Read",
		Importance: 5, Confidence: 0.3,
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Confidence = 1.5
	if err := c.Validate(); err == nil {
		t.Error("want error for confidence > 1")
	}
}

func TestMemoryEnums_RoundTrip(t *testing.T) {
	m := Memory{
		ID: "mem-1", Type: MemoryArchitecture, Tier: TierSemantic,
		Title: "Use jose", Strength: 0.7,
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out Memory
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Type != MemoryArchitecture || out.Tier != TierSemantic {
		t.Errorf("got %+v", out)
	}
}

func TestSessionStatus_Rejection(t *testing.T) {
	var s SessionStatus
	err := json.Unmarshal([]byte(`"sleeping"`), &s)
	if err == nil || !strings.Contains(err.Error(), "SessionStatus") {
		t.Errorf("want enum error, got %v", err)
	}
}

func TestLease_IsExpired(t *testing.T) {
	now := time.Now()
	l := &Lease{ActionID: "a-1", HolderID: "h-1", GrantedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Minute)}
	if l.IsExpired(now) {
		t.Error("not expired yet")
	}
	if !l.IsExpired(now.Add(2 * time.Minute)) {
		t.Error("should be expired")
	}
	var nilLease *Lease
	if !nilLease.IsExpired(now) {
		t.Error("nil lease must be expired")
	}
}
