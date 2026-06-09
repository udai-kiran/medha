package api

import (
	"encoding/json"
	"testing"
)

// TestCompressedCallback_DecodesAbbreviations locks the inbound JSON key the
// Python /compress response uses to return detected abbreviations. If this key
// drifts, the glossary merge silently no-ops while all other tests still pass.
func TestCompressedCallback_DecodesAbbreviations(t *testing.T) {
	body := `{
		"id": "obs-1",
		"sessionId": "sess-1",
		"type": "command",
		"title": "set up sso",
		"importance": 5,
		"abbreviations": {"SSO": "Single Sign-On", "MFA": "Multi-Factor Authentication"}
	}`
	var cb CompressedCallback
	if err := json.Unmarshal([]byte(body), &cb); err != nil {
		t.Fatal(err)
	}
	if cb.Abbreviations["SSO"] != "Single Sign-On" {
		t.Errorf("SSO = %q, want Single Sign-On", cb.Abbreviations["SSO"])
	}
	if cb.Abbreviations["MFA"] != "Multi-Factor Authentication" {
		t.Errorf("MFA = %q, want Multi-Factor Authentication", cb.Abbreviations["MFA"])
	}
}

// TestCompressedCallback_NoAbbreviations confirms the field is simply empty
// (not an error) when the LLM returns none.
func TestCompressedCallback_NoAbbreviations(t *testing.T) {
	var cb CompressedCallback
	if err := json.Unmarshal([]byte(`{"id":"o","sessionId":"s","title":"t"}`), &cb); err != nil {
		t.Fatal(err)
	}
	if len(cb.Abbreviations) != 0 {
		t.Errorf("expected no abbreviations, got %v", cb.Abbreviations)
	}
}
