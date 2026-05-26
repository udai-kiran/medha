// Package dedup drops duplicate observations within a rolling window so the
// storage and search indexes stay clean (FR-3, NFR-8).
//
// The dedup key is `SHA-256(sessionId + toolName + canonicalJSON(toolInput))`
// — same tool with same canonical input within a session is treated as a
// duplicate. Canonical JSON (RFC 8785-style: sort keys, no whitespace) makes
// the hash robust to map iteration order and pretty-printing differences.
package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ComputeKey returns the dedup hash for an observation. toolInput is any
// JSON-serialisable Go value; callers typically pass json.RawMessage from
// the inbound payload.
func ComputeKey(sessionID, toolName string, toolInput any) (string, error) {
	canonical, err := canonicalJSON(toolInput)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(toolName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canonicalJSON returns a deterministic JSON encoding of v (sorted keys,
// no whitespace). Accepts:
//   - json.RawMessage / []byte: parsed and re-encoded canonically.
//   - any Go value: encoded with json.Marshal then re-canonicalised.
//
// Nil yields `null` so callers without a tool input still hash deterministically.
func canonicalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}

	var raw []byte
	switch t := v.(type) {
	case json.RawMessage:
		if len(t) == 0 {
			return []byte("null"), nil
		}
		raw = t
	case []byte:
		if len(t) == 0 {
			return []byte("null"), nil
		}
		raw = t
	default:
		// Marshal then re-canonicalise so the canonical pass owns ordering.
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("canonicalJSON: %w", err)
		}
		raw = b
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// Not valid JSON — hash the raw bytes verbatim so we still get a
		// deterministic key rather than failing the capture path.
		return raw, nil
	}
	return marshalCanonical(parsed)
}

// marshalCanonical recursively encodes v with sorted map keys, no spaces.
func marshalCanonical(v any) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			kb, _ := json.Marshal(k)
			out = append(out, kb...)
			out = append(out, ':')
			vb, err := marshalCanonical(t[k])
			if err != nil {
				return nil, err
			}
			out = append(out, vb...)
		}
		out = append(out, '}')
		return out, nil
	case []any:
		out := []byte{'['}
		for i, e := range t {
			if i > 0 {
				out = append(out, ',')
			}
			eb, err := marshalCanonical(e)
			if err != nil {
				return nil, err
			}
			out = append(out, eb...)
		}
		out = append(out, ']')
		return out, nil
	default:
		// Scalars, strings, numbers, bools, nil — json.Marshal already gives
		// a canonical form for these.
		return json.Marshal(v)
	}
}
