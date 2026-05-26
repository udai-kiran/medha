// Package search owns the indexes and ranking primitives: BM25 (this file +
// bm25.go), vector (Task 15), graph (Task 16), and the RRF orchestrator
// (Task 17). All three single-modality engines satisfy the SearchEngine
// interface so the hybrid path can call them uniformly.
package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tokenize lower-cases input, splits on non-letter/digit runes, and drops
// very-short and stopwords. It also segments CJK runs by rune so Japanese
// and Chinese match without whitespace-aware tokenization.
//
// Result is allocated once; callers should not mutate.
func Tokenize(text string) []string {
	if text == "" {
		return nil
	}
	out := make([]string, 0, 16)
	var b strings.Builder
	b.Grow(len(text))

	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size

		if isCJK(r) {
			// CJK: emit the previous Latin/digit buffer and then the rune itself.
			if b.Len() > 0 {
				out = appendToken(out, b.String())
				b.Reset()
			}
			out = appendToken(out, string(unicode.ToLower(r)))
			continue
		}

		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		if b.Len() > 0 {
			out = appendToken(out, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		out = appendToken(out, b.String())
	}
	return out
}

func appendToken(out []string, tok string) []string {
	if len(tok) < 2 {
		return out
	}
	if _, ok := stopwords[tok]; ok {
		return out
	}
	return append(out, stem(tok))
}

// stem applies a very small heuristic stemmer (suffix stripping). A full
// Porter stemmer would be more accurate but pulls in a dependency we don't
// otherwise need; the cheap version is plenty for code-keyword recall.
func stem(t string) string {
	switch {
	case strings.HasSuffix(t, "ies") && len(t) > 4:
		return t[:len(t)-3] + "y"
	case strings.HasSuffix(t, "sses") && len(t) > 5:
		return t[:len(t)-2]
	case strings.HasSuffix(t, "ing") && len(t) > 5:
		return t[:len(t)-3]
	case strings.HasSuffix(t, "ed") && len(t) > 4:
		return t[:len(t)-2]
	case strings.HasSuffix(t, "es") && len(t) > 3:
		return t[:len(t)-1]
	case strings.HasSuffix(t, "s") && len(t) > 3 && !strings.HasSuffix(t, "ss"):
		return t[:len(t)-1]
	}
	return t
}

// isCJK is true for the Hiragana/Katakana/CJK Unified Ideographs blocks.
func isCJK(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x309F: // Hiragana
		return true
	case r >= 0x30A0 && r <= 0x30FF: // Katakana
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
		return true
	}
	return false
}

// stopwords are removed before indexing/querying. Small list — favour recall.
var stopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {},
	"if": {}, "then": {}, "else": {}, "for": {}, "to": {}, "of": {},
	"in": {}, "on": {}, "at": {}, "by": {}, "is": {}, "are": {},
	"was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"have": {}, "has": {}, "had": {}, "do": {}, "does": {}, "did": {},
	"this": {}, "that": {}, "with": {}, "from": {}, "as": {}, "it": {},
}

// synonyms expands query tokens with simple coding-domain equivalents.
// Bidirectional pairs are exploded so either side matches the other.
var synonyms = map[string][]string{
	"auth":           {"authentication", "authz"},
	"authentication": {"auth"},
	"db":             {"database"},
	"database":       {"db"},
	"fn":             {"function"},
	"function":       {"fn"},
	"api":            {"endpoint"},
	"endpoint":       {"api"},
	"jwt":            {"token"},
}

// ExpandSynonyms returns terms plus their bidirectional synonyms,
// deduplicated. Used on query terms only — index terms stay as-is.
func ExpandSynonyms(terms []string) []string {
	if len(terms) == 0 {
		return terms
	}
	seen := make(map[string]struct{}, len(terms)*2)
	out := make([]string, 0, len(terms)*2)
	add := func(s string) {
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, t := range terms {
		add(t)
		for _, s := range synonyms[t] {
			add(s)
		}
	}
	return out
}
