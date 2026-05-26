"""Heuristic relationship extraction.

Maps verbs/predicates to the typed edges agent_mem cares about:

| Predicate                          | Edge type     |
|------------------------------------|---------------|
| depends on / imports / uses / requires | DEPENDS_ON   |
| implements / extends                | IMPLEMENTS   |
| works at / employed by / member of | WORKS_AT     |
| supersedes / replaces / deprecates | SUPERSEDES   |
| contradicts / conflicts with       | CONTRADICTS  |
| derived from / based on / forked from | DERIVED_FROM |

Anything else with both an explicit subject and object falls back to
RELATED_TO so we still build a sparse graph from observations a real
relationship extractor (GLiREL, LLM) hasn't seen yet.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from medha.extraction.heuristic_extractor import HeuristicExtractor
from medha.models import Relationship


_VERB_TO_TYPE: dict[str, str] = {
    "depends on": "DEPENDS_ON",
    "imports": "DEPENDS_ON",
    "uses": "DEPENDS_ON",
    "requires": "DEPENDS_ON",
    "implements": "IMPLEMENTS",
    "extends": "IMPLEMENTS",
    "works at": "WORKS_AT",
    "employed by": "WORKS_AT",
    "member of": "WORKS_AT",
    "supersedes": "SUPERSEDES",
    "replaces": "SUPERSEDES",
    "deprecates": "SUPERSEDES",
    "contradicts": "CONTRADICTS",
    "conflicts with": "CONTRADICTS",
    "derived from": "DERIVED_FROM",
    "based on": "DERIVED_FROM",
    "forked from": "DERIVED_FROM",
}


@dataclass
class _Pattern:
    verb: str
    rel: str
    regex: re.Pattern[str]


def _compile_patterns() -> list[_Pattern]:
    # Each pattern captures a "subject phrase" followed by the verb and an
    # "object phrase". Phrases are bounded by punctuation or sentence breaks.
    out: list[_Pattern] = []
    for verb, rel in _VERB_TO_TYPE.items():
        # Word boundary on each side; allow filler words between subject and verb.
        pattern = rf"([A-Za-z_][\w./]*?(?:\s+[A-Za-z_][\w./]*?){{0,3}})\s+{re.escape(verb)}\s+([A-Za-z_][\w./]*?(?:\s+[A-Za-z_][\w./]*?){{0,3}})(?=[\s,.;:]|$)"
        out.append(_Pattern(verb=verb, rel=rel, regex=re.compile(pattern, re.IGNORECASE)))
    return out


_PATTERNS = _compile_patterns()


class HeuristicRelationshipExtractor:
    """Verb-keyword relationship extractor."""

    name = "heuristic_relations"

    def __call__(self, text: str, *, source_observation_id: str | None = None) -> list[Relationship]:
        if not text:
            return []
        out: list[Relationship] = []
        seen: set[tuple[str, str, str]] = set()

        for pat in _PATTERNS:
            for m in pat.regex.finditer(text):
                src = _clean(m.group(1))
                tgt = _clean(m.group(2))
                if not src or not tgt or src.lower() == tgt.lower():
                    continue
                key = (src.lower(), tgt.lower(), pat.rel)
                if key in seen:
                    continue
                seen.add(key)
                out.append(
                    Relationship(
                        source=src,
                        target=tgt,
                        type=pat.rel,
                        confidence=0.6,
                        sourceObservationId=source_observation_id,
                    )
                )
        return out


def _clean(phrase: str) -> str:
    # Strip leading articles + trailing punctuation; rejects empty phrases.
    phrase = phrase.strip(" \t\n.,;:")
    for prefix in ("the ", "a ", "an "):
        if phrase.lower().startswith(prefix):
            phrase = phrase[len(prefix) :]
    return phrase


def co_occurrence_relationships(
    entities: list[str], *, source_observation_id: str | None = None
) -> list[Relationship]:
    """Fallback: emit RELATED_TO between every pair of entities that
    co-occur in the same text, up to a small cap.

    This is the spare graph builder that runs when the verb patterns find
    nothing — better than an empty graph, weak confidence so a real
    extractor can override.
    """
    out: list[Relationship] = []
    # Cap: O(n^2) pair count when entities are many is wasteful.
    if len(entities) > 8:
        entities = entities[:8]
    for i, a in enumerate(entities):
        for b in entities[i + 1 :]:
            if a.lower() == b.lower():
                continue
            out.append(
                Relationship(
                    source=a,
                    target=b,
                    type="RELATED_TO",
                    confidence=0.3,
                    sourceObservationId=source_observation_id,
                )
            )
    return out


def extract_relationships(
    text: str, *, source_observation_id: str | None = None
) -> list[Relationship]:
    """Run the heuristic extractor; fall back to co-occurrence if nothing matched."""
    rels = HeuristicRelationshipExtractor()(text, source_observation_id=source_observation_id)
    if rels:
        return rels
    ents = HeuristicExtractor()(text, source_observation_id=source_observation_id)
    names = [e.name for e in ents]
    return co_occurrence_relationships(names, source_observation_id=source_observation_id)
