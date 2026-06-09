package state

import "testing"

func TestGlossaryKey_RoundTrip(t *testing.T) {
	prefix := glossaryPrefix("proj")
	key := glossaryKey("proj", "MFA")
	if got := abbrevFromKey(prefix, key); got != "MFA" {
		t.Fatalf("round-trip: got %q want %q (key=%q prefix=%q)", got, "MFA", key, prefix)
	}
}

func TestGlossaryPrefix_NoCollisionAcrossProjects(t *testing.T) {
	// The trailing ':' must stop project "foo" from matching keys of "foobar".
	prefix := glossaryPrefix("foo")
	siblingKey := glossaryKey("foobar", "MFA")
	if abbrevFromKey(prefix, siblingKey) != "" {
		t.Fatalf("prefix %q wrongly matched sibling project key %q", prefix, siblingKey)
	}
	// And it must still match its own project's keys.
	ownKey := glossaryKey("foo", "MFA")
	if abbrevFromKey(prefix, ownKey) != "MFA" {
		t.Fatalf("prefix %q failed to match own key %q", prefix, ownKey)
	}
}

func TestAbbrevFromKey_NonMatch(t *testing.T) {
	if got := abbrevFromKey("abbreviations:proj:", "something:else:X"); got != "" {
		t.Fatalf("expected empty for non-matching key, got %q", got)
	}
}

func TestValidAbbrev(t *testing.T) {
	cases := []struct {
		name      string
		abbrev    string
		expansion string
		want      bool
	}{
		{"ok", "MFA", "Multi-Factor Authentication", true},
		{"empty abbrev", "", "Something", false},
		{"empty expansion", "MFA", "", false},
		{"abbrev too short", "M", "Money", false},
		{"abbrev too long", "ABCDEFGHIJKLMNOPQ", "long thing here", false},
		{"expansion not longer", "API", "app", false},
		{"expansion equal foldcase", "api", "API", false},
		{"abbrev with colon", "M:F", "Multi Factor", false},
		{"abbrev with space", "M F", "Multi Factor", false},
		{"expansion clipped too long", "XY", string(make([]byte, 0)) + repeat("a", 201), false},
		{"expansion exactly at cap ok", "XY", repeat("a", 200), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validAbbrev(c.abbrev, c.expansion); got != c.want {
				t.Errorf("validAbbrev(%q,%q)=%v want %v", c.abbrev, c.expansion, got, c.want)
			}
		})
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
