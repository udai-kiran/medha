// Package privacy strips secrets and marked-private content from observations
// before any persistence or downstream LLM call (FR-6/7/8, NFR-10).
//
// The filter is fail-closed: on regex error it redacts conservatively rather
// than passing the input through. Tasks 8 and 13 wire it before storage and
// before any external call.
package privacy

import "regexp"

// redactedToken is the placeholder we substitute for any matched secret.
// Kept short and obvious so a human auditing the DB can spot leaks fast.
const redactedToken = "***REDACTED***"

// secretPatterns lists every regex that triggers redaction. Each entry has a
// name (used only in tests / logs) and a compiled pattern.
//
// Add new providers by appending; order does not matter — every pattern runs.
// Patterns are intentionally a superset (some false positives are acceptable;
// false negatives are not — that would leak a secret).
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	// Anthropic API keys: `sk-ant-...`.
	{"anthropic_api_key", regexp.MustCompile(`(?i)sk-ant-[A-Za-z0-9_\-]{20,}`)},
	// OpenAI API keys: classic `sk-` and project-scoped `sk-proj-`.
	{"openai_api_key", regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_\-]{20,}`)},
	// GitHub personal/access/oauth tokens.
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`)},
	// AWS access key IDs.
	{"aws_access_key_id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	// AWS secret access keys (heuristic: 40 base64 chars after an obvious marker).
	{"aws_secret_access_key", regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*"?[A-Za-z0-9/+=]{40}"?`)},
	// Slack tokens.
	{"slack_token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	// Google API keys: prefix `AIza` + 35+ url-safe chars.
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35,}\b`)},
	// Generic key/value secret assignments. The full assignment is redacted,
	// not just the value, so the surrounding name isn't left dangling.
	{"generic_key_value", regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|token|secret|api[_-]?key|bearer)\s*[:=]\s*["']?[^\s"',;]{4,}["']?`)},
	// Authorization: Bearer <token> headers.
	{"bearer_header", regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+[A-Za-z0-9._\-]{8,}`)},
	// PEM-encoded private keys.
	{"private_key_block", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----`)},
	// JWTs (header.payload.signature). 8+ chars per segment to dodge most words.
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\b`)},
}

// privateBlock removes `<private>...</private>` blocks entirely (DOTALL via (?s)).
// Multi-line content is supported because tool outputs frequently span lines.
var privateBlock = regexp.MustCompile(`(?s)<private>.*?</private>`)

// ansiEscape strips ANSI escape codes (CSI + OSC). Two-pass for safety.
//
// The first pattern handles CSI (Control Sequence Introducer): ESC [ ... letter.
// The second handles SGR / OSC and bare ESC + char fallbacks.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
