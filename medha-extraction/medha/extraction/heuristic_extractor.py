"""Regex+heuristic entity extractor — runs without external NLP deps.

What it catches:
  - File paths (recycles compression's _FILE_PATH_RE).
  - Identifier-like names (camelCase, snake_case, PascalCase) — likely
    functions / classes.
  - URLs and email addresses (always OBJECT/URL or OBJECT/EMAIL).
  - Title-cased multi-word noun phrases — likely people or orgs.

Confidence is intentionally lower than spaCy/GLiNER would assign (0.5-0.7)
so a downstream LLM/spaCy stage can supersede.
"""

from __future__ import annotations

import re
from collections.abc import Iterable

from medha.extraction.types import classify_subtype
from medha.models import Entity

_PASCAL_RE = re.compile(r"\b[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]+){1,}\b")
_CAMEL_RE = re.compile(r"\b[a-z]+(?:[A-Z][a-z0-9]+){1,}\b")
_SNAKE_RE = re.compile(r"\b[a-z][a-z0-9]+(?:_[a-z0-9]+){1,}\b")
_TITLE_PHRASE_RE = re.compile(r"\b(?:[A-Z][a-z]+(?:\s+[A-Z][a-z]+){0,3})\b")
_URL_RE = re.compile(r"https?://[^\s\"'<>)]+")
_EMAIL_RE = re.compile(r"\b[\w.\-]+@[\w.\-]+\.\w{2,}\b")
# Recycle the file regex from compression to keep one source of truth.
from medha.compression.synthetic_compressor import _FILE_PATH_RE  # noqa: E402


def _dedup_keep_order(items: Iterable[str]) -> list[str]:
    seen: dict[str, None] = {}
    for x in items:
        if x and x not in seen:
            seen[x] = None
    return list(seen.keys())


class HeuristicExtractor:
    """Regex-based extractor; baseline confidence ≈ 0.55."""

    name = "heuristic"

    def __call__(self, text: str, *, source_observation_id: str | None = None) -> list[Entity]:
        if not text:
            return []
        out: list[Entity] = []

        files = _dedup_keep_order(_FILE_PATH_RE.findall(text))
        for f in files:
            out.append(
                Entity(
                    name=f,
                    type="OBJECT",
                    subtype="FILE",
                    confidence=0.7,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        for fn in _dedup_keep_order(_CAMEL_RE.findall(text)):
            # Skip if already captured as a file.
            if any(fn in f for f in files):
                continue
            out.append(
                Entity(
                    name=fn,
                    type="OBJECT",
                    subtype="FUNCTION",
                    confidence=0.6,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        for cls in _dedup_keep_order(_PASCAL_RE.findall(text)):
            if any(cls in f for f in files):
                continue
            out.append(
                Entity(
                    name=cls,
                    type="OBJECT",
                    subtype="CLASS",
                    confidence=0.55,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        for snake in _dedup_keep_order(_SNAKE_RE.findall(text)):
            out.append(
                Entity(
                    name=snake,
                    type="OBJECT",
                    subtype="IDENTIFIER",
                    confidence=0.55,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        for url in _dedup_keep_order(_URL_RE.findall(text)):
            out.append(
                Entity(
                    name=url,
                    type="OBJECT",
                    subtype="URL",
                    confidence=0.85,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        for email in _dedup_keep_order(_EMAIL_RE.findall(text)):
            out.append(
                Entity(
                    name=email,
                    type="OBJECT",
                    subtype="EMAIL",
                    confidence=0.9,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        # Title-cased phrases come last so they don't crowd out the
        # higher-confidence matches above; we also skip anything we've already
        # captured to avoid duplicate names with different types.
        already = {e.name.lower() for e in out}
        for phrase in _dedup_keep_order(_TITLE_PHRASE_RE.findall(text)):
            if phrase.lower() in already:
                continue
            # Single-word title-case is too noisy — require a space.
            if " " not in phrase:
                continue
            out.append(
                Entity(
                    name=phrase,
                    type="PERSON",  # weak guess; LLM/spaCy will refine
                    confidence=0.4,
                    sourceObservationIds=[source_observation_id] if source_observation_id else [],
                )
            )

        # Apply subtype refinement for entries that didn't get one above.
        for i, e in enumerate(out):
            if e.subtype is None:
                refined = classify_subtype(e.name, e.type)
                if refined:
                    out[i] = e.model_copy(update={"subtype": refined})

        return out
