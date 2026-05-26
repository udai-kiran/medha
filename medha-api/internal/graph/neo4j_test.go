package graph

import "testing"

// Real Neo4j integration tests live in the integration suite (Task 34) — they
// need a running Bolt endpoint. The unit tests here cover the parts that
// don't touch the network: label sanitisation, prop reading, type checks.

func TestSanitiseLabel(t *testing.T) {
	cases := map[string]string{
		"DEPENDS_ON":    "DEPENDS_ON",
		"works at":      "worksat",
		"a; DROP TABLE": "aDROPTABLE",
		"":              "RELATED_TO",
		"123-abc":       "123abc",
	}
	for in, want := range cases {
		if got := sanitiseLabel(in); got != want {
			t.Errorf("sanitiseLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStringProp(t *testing.T) {
	props := map[string]any{"a": "hello", "b": 7}
	if stringProp(props, "a") != "hello" {
		t.Error("string ok")
	}
	if stringProp(props, "b") != "" {
		t.Error("non-string should yield empty")
	}
	if stringProp(props, "missing") != "" {
		t.Error("missing should yield empty")
	}
}
