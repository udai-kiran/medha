package privacy

// StripANSI removes ANSI escape sequences from s.
//
// Called after privacy redaction so that visible secrets aren't hidden behind
// escape codes (e.g. a coloured key=value in a tool output).
func StripANSI(s string) string {
	if s == "" {
		return s
	}
	return ansiEscape.ReplaceAllString(s, "")
}
