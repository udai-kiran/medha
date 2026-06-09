package state

import (
	"context"
	"strings"
)

// The abbreviation glossary is a per-project map of abbreviation → expansion,
// learned by the LLM during compression. The hot path is:
//
//  1. The worker fetches the project's glossary and ships it to Python
//     /compress, which inline-expands the observation text the LLM sees.
//  2. The LLM returns any newly-detected pairs; the compression callback merges
//     them back in via MergeGlossary (first-write-wins).
//
// Storage is one KV row per abbreviation (scope=abbreviations) so concurrent
// merges of *different* abbreviations don't clobber each other.

const (
	// glossaryMaxEntries caps the stored glossary per project. Beyond this we
	// stop accepting new abbreviations — the glossary is a convenience, not an
	// unbounded sink.
	glossaryMaxEntries = 500
	// glossaryMaxAbbrevLen / MinAbbrevLen bound what counts as an abbreviation
	// key. Long strings are almost certainly not abbreviations.
	glossaryMaxAbbrevLen = 16
	glossaryMinAbbrevLen = 2
	// glossaryMaxExpansionLen clips runaway expansions.
	glossaryMaxExpansionLen = 200
)

// glossaryPrefix is the ListByPrefix prefix for a project's abbreviations. The
// trailing ':' is essential: without it, project "foo" would also match the
// keys of project "foobar".
func glossaryPrefix(project string) string {
	return string(Key(ScopeAbbreviations, project, "")) + ":"
}

// glossaryKey is the canonical KV key for a single (project, abbrev) entry.
func glossaryKey(project, abbrev string) string {
	return Key(ScopeAbbreviations, project, abbrev)
}

// abbrevFromKey recovers the abbreviation from a full KV key produced by
// glossaryKey, given the matching glossaryPrefix. Returns "" if key doesn't
// carry the prefix (defensive — ListByPrefix should only ever return matches).
func abbrevFromKey(prefix, key string) string {
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return key[len(prefix):]
}

// validAbbrev reports whether (abbrev, expansion) is worth storing. We require a
// short-ish abbreviation, a non-empty expansion that actually differs from and
// is longer than the abbreviation, and reject anything containing ':' (which
// would corrupt the KV key namespace).
func validAbbrev(abbrev, expansion string) bool {
	abbrev = strings.TrimSpace(abbrev)
	expansion = strings.TrimSpace(expansion)
	if abbrev == "" || expansion == "" {
		return false
	}
	if strings.ContainsAny(abbrev, ": \t\n") {
		return false
	}
	n := len([]rune(abbrev))
	if n < glossaryMinAbbrevLen || n > glossaryMaxAbbrevLen {
		return false
	}
	if len([]rune(expansion)) > glossaryMaxExpansionLen {
		return false
	}
	// The expansion must add information: a different, longer string.
	if strings.EqualFold(abbrev, expansion) || len(expansion) <= len(abbrev) {
		return false
	}
	return true
}

// GetGlossary returns the project's abbreviation→expansion map. An empty
// (non-nil) map is returned when the project has no learned abbreviations.
func (s *Store) GetGlossary(ctx context.Context, project string) (map[string]string, error) {
	kv := NewKV(s)
	prefix := glossaryPrefix(project)
	rows, err := kv.ListByPrefix(ctx, ScopeAbbreviations, prefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for key, blob := range rows {
		abbrev := abbrevFromKey(prefix, key)
		if abbrev == "" {
			continue
		}
		// Values are JSON-encoded strings (KV.Put marshals).
		out[abbrev] = strings.Trim(blob, `"`)
	}
	return out, nil
}

// MergeGlossary adds newly-learned pairs to the project's glossary using
// first-write-wins: an abbreviation already present is left untouched, so a
// later (possibly worse) expansion can't overwrite an established one. Invalid
// pairs are skipped, and the glossary is capped at glossaryMaxEntries. Returns
// the number of new entries actually written.
func (s *Store) MergeGlossary(ctx context.Context, project string, pairs map[string]string) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	existing, err := s.GetGlossary(ctx, project)
	if err != nil {
		return 0, err
	}
	kv := NewKV(s)
	count := len(existing)
	added := 0
	for abbrev, expansion := range pairs {
		abbrev = strings.TrimSpace(abbrev)
		expansion = strings.TrimSpace(expansion)
		if !validAbbrev(abbrev, expansion) {
			continue
		}
		if _, ok := existing[abbrev]; ok {
			continue // first-write-wins
		}
		if count >= glossaryMaxEntries {
			break
		}
		if err := kv.Put(ctx, ScopeAbbreviations, glossaryKey(project, abbrev), expansion); err != nil {
			return added, err
		}
		existing[abbrev] = expansion
		count++
		added++
	}
	return added, nil
}
