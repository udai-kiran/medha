package privacy

import "strings"

// FilterResult describes what Filter did.
type FilterResult struct {
	// HadSecrets is true when at least one secret pattern matched. The caller
	// records this on the observation (RawObservation.HasSecrets) so downstream
	// enrichment skips it (FR-9).
	HadSecrets bool
	// HitPatterns lists the names of patterns that fired (for tests / logs).
	HitPatterns []string
}

// Filter applies the layered privacy pipeline in the correct order:
//  1. Strip `<private>...</private>` blocks first — anything inside should
//     never be persisted, even if it contained additional secrets we'd have
//     redacted next.
//  2. Redact secret patterns (API keys, key=value assignments, JWTs, ...).
//  3. Strip ANSI escape codes so visible secrets aren't hidden behind colour.
//
// Returns the sanitised text and a FilterResult describing what was found.
// Errors are not returned: the filter is fail-closed by design — any pattern
// it cannot evaluate would simply not redact, but that scenario doesn't arise
// because all patterns are MustCompiled at init.
func Filter(s string) (string, FilterResult) {
	if s == "" {
		return s, FilterResult{}
	}

	res := FilterResult{}

	// 1. Remove <private> blocks first.
	if strings.Contains(s, "<private>") {
		s = privateBlock.ReplaceAllString(s, "")
	}

	// 2. Redact known secret formats.
	for _, p := range secretPatterns {
		if loc := p.re.FindStringIndex(s); loc != nil {
			res.HadSecrets = true
			res.HitPatterns = append(res.HitPatterns, p.name)
			s = p.re.ReplaceAllString(s, redactedToken)
		}
	}

	// 3. Strip ANSI last.
	s = StripANSI(s)

	return s, res
}

// FilterBytes is a convenience wrapper for byte slices (e.g. raw JSON
// payloads). It allocates only when a substitution happens.
func FilterBytes(b []byte) ([]byte, FilterResult) {
	out, res := Filter(string(b))
	if out == string(b) && !res.HadSecrets {
		return b, res
	}
	return []byte(out), res
}
