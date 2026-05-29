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
	// Fast path: pure string scans have no mutex overhead (unlike regexp under
	// -race). Only enter the regex loop when a secret hint is detected.
	if hasSecretHint(s) {
		for _, p := range secretPatterns {
			if loc := p.re.FindStringIndex(s); loc != nil {
				res.HadSecrets = true
				res.HitPatterns = append(res.HitPatterns, p.name)
				s = p.re.ReplaceAllString(s, redactedToken)
			}
		}
	}

	// 3. Strip ANSI last; skip when no escape byte is present.
	if strings.ContainsRune(s, '\x1b') {
		s = StripANSI(s)
	}

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

// hasSecretHint returns true when s contains a substring that could be part of
// a secret matching one of the secretPatterns. It is intentionally conservative:
// false positives (normal text that triggers the hint) are acceptable — they
// just cause the regex loop to run unnecessarily. False negatives would silently
// skip redaction, which is not allowed.
//
// Pure string operations are used deliberately: regexp operations acquire a
// mutex internally, which is prohibitively expensive under -race for the common
// (clean-input) case.
func hasSecretHint(s string) bool {
	// Case-sensitive prefixes that uniquely identify most secret formats.
	if strings.Contains(s, "sk-") ||
		strings.Contains(s, "AKIA") ||
		strings.Contains(s, "AIza") ||
		strings.Contains(s, "eyJ") ||
		strings.Contains(s, "xox") ||
		strings.Contains(s, "-----BEGIN") ||
		strings.Contains(s, "ghp_") ||
		strings.Contains(s, "gho_") ||
		strings.Contains(s, "ghu_") ||
		strings.Contains(s, "ghs_") ||
		strings.Contains(s, "ghr_") {
		return true
	}
	// Case-insensitive keyword checks (generic_key_value, bearer_header, aws).
	lower := strings.ToLower(s)
	return strings.Contains(lower, "password") ||
		strings.Contains(lower, "passwd") ||
		strings.Contains(lower, "pwd") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "bearer") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "aws_secret")
}
