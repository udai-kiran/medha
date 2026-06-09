package consolidation

import (
	"encoding/json"
	"testing"
)

// TestCompressRequest_WireContract locks the JSON keys the Python /compress
// route reads. A rename here would silently disable abbreviation expansion
// while every other test still passes, so pin the contract explicitly.
func TestCompressRequest_WireContract(t *testing.T) {
	req := compressRequest{
		ID:        "obs-1",
		SessionID: "sess-1",
		HookType:  "post_tool_use",
		Project:   "proj-a",
		Glossary:  map[string]string{"MFA": "Multi-Factor Authentication"},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["project"] != "proj-a" {
		t.Errorf("project key = %v, want proj-a", m["project"])
	}
	g, ok := m["glossary"].(map[string]any)
	if !ok || g["MFA"] != "Multi-Factor Authentication" {
		t.Errorf("glossary key = %v, want MFA mapping", m["glossary"])
	}
}

// TestCompressRequest_OmitsEmptyGlossary ensures a disabled/empty glossary
// doesn't bloat the payload (omitempty).
func TestCompressRequest_OmitsEmptyGlossary(t *testing.T) {
	b, _ := json.Marshal(compressRequest{ID: "x", SessionID: "s", HookType: "h"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, present := m["glossary"]; present {
		t.Errorf("empty glossary should be omitted, got %v", m["glossary"])
	}
	if _, present := m["project"]; present {
		t.Errorf("empty project should be omitted, got %v", m["project"])
	}
}
