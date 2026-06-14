"""LLM-driven compression with synthetic fallback (FR-12).

The flow:
  1. Build a system+user prompt from the RawObservation.
  2. Call the configured LLM with json_mode=True (response_format: json_object).
  3. Parse the JSON response into a CompressedObservation via json.loads + Pydantic.
  4. On any failure (timeout, parse error, no API key), fall back to the
     synthetic path so the pipeline never blocks.
"""

from __future__ import annotations

import asyncio
import json
import re
from dataclasses import dataclass

import structlog

from medha.compression.synthetic_compressor import synthetic_compress
from medha.config import Settings
from medha.llm.client import LLMClient as LLMClient  # re-exported for back-compat
from medha.models import CompressedObservation, RawObservation
from medha.utils.validators import clip

logger = structlog.get_logger(__name__)


@dataclass(frozen=True)
class LLMCompressorConfig:
    """Per-call knobs for the LLM compressor."""

    timeout_s: float = 60.0
    max_tokens: int = 1024


# The abbreviation-detection instruction is always present; the feature is gated
# on the Go side (the worker skips sending the glossary and the callback skips
# the merge when ABBREVIATION_EXPANSION_ENABLED=false). The only cost when
# disabled is a few prompt tokens, which we accept rather than threading a
# per-request "detect" flag through the wire.
_SYSTEM_PROMPT = """\
You compress software agent observations into structured JSON for a long-term memory system.
The output feeds semantic search, entity extraction, and session summarisation — be specific
and searchable, not generic. Every field should help a future agent recall why this mattered.

TYPE — pick the single best match:
  file_read · file_edit · command · test · search · decision · error · discovery · other

TITLE — verb-object form, max 120 chars. Mention the key artefact.
  Good: "Fixed JWT expiry bug in auth middleware"  "Read Postgres schema migration"
  Bad:  "The agent read a file"  "Command executed"

SUBTITLE — one-line qualification when the title alone is ambiguous (e.g. the affected
  file path, the error type, or the test suite name). Omit if the title is self-contained.

FACTS — atomic, standalone assertions a future agent can use without surrounding context.
  Each fact must make sense recalled in isolation: "The jose library handles JWT validation
  in src/auth.ts" not "It uses jose". Aim for 2-5 facts. Never repeat the narrative.

NARRATIVE — 1-2 sentences: what happened and why it matters. Complements the facts;
  does not duplicate them. Empty string if the observation is too low-signal to narrate.

CONCEPTS — technical terms that enable search: library names, tool names, file/module
  names, design patterns, domain concepts, error codes. NOT generic nouns (file, code,
  function, output, result). Max 10, ordered by relevance.

FILES — only paths that appear verbatim in the observation input or output.
  Never infer, reconstruct, or guess a path. Empty array if none are present.

IMPORTANCE — integer 0-10:
  0-2  trivial  : navigation, listing dirs, reading unchanged config, no-op commands
  3-5  routine  : reading code, running passing tests, simple informational commands
  6-8  significant: file edits, bug fixes, new dependencies added, test failures diagnosed
  9-10 critical : architectural decisions, security findings, data-loss risks, major discoveries

ABBREVIATIONS — detect abbreviation→expansion pairs present in or strongly implied by the
  text. Only include when the expansion appears verbatim in the observation or is
  unambiguous (e.g. JWT→JSON Web Token, CI→Continuous Integration). Never guess. Max 20.

LOW-SIGNAL RULE: if the observation is pure navigation, has empty/trivial output, or
  conveys no durable information, set importance ≤ 2 and leave facts/concepts/files empty
  rather than manufacturing content.

Respond ONLY with a JSON object. No prose, no markdown fences."""

_USER_TEMPLATE = """\
Observation:
  hook: {hook}
  tool: {tool}
  input: {input}
  output: {output}

Produce a JSON object with exactly these keys:
{{
  "type": "file_read|file_edit|command|test|search|decision|error|discovery|other",
  "title": "verb-object title (max 120 chars)",
  "subtitle": "one-line qualification or empty string",
  "facts": ["standalone atomic fact", ...],
  "narrative": "1-2 sentences or empty string",
  "concepts": ["searchable technical term", ...],
  "files": ["verbatim/path/from/observation", ...],
  "importance": 5,
  "abbreviations": {{"ABBR": "Full Expansion", ...}}
}}"""

# Cap on abbreviation pairs accepted from a single LLM response, so a bad
# response can't flood the glossary merge.
_MAX_ABBREVIATIONS = 20


def expand_abbreviations(text: str, glossary: dict[str, str]) -> str:
    """Inline-expand known abbreviations the first time each appears in ``text``.

    For each ``abbr → expansion`` pair, the first whole-token, case-sensitive
    occurrence of ``abbr`` becomes ``abbr (expansion)``. Occurrences the author
    already parenthesised (``abbr (...)``) are left untouched so we never produce
    ``MFA (X) (X)``. Substrings (e.g. ``API`` inside ``RAPID``) never match.
    """
    if not text or not glossary:
        return text
    for abbr, expansion in glossary.items():
        if not abbr or not expansion:
            continue
        # Alphanumeric lookarounds (not \b) so symbol-bearing abbrevs like "C++"
        # still anchor correctly; (?!\s*\() skips already-parenthesised abbrevs;
        # count=1 expands only the first mention.
        pattern = (
            r"(?<![A-Za-z0-9])" + re.escape(abbr) + r"(?![A-Za-z0-9])(?!\s*\()"
        )
        text = re.sub(pattern, f"{abbr} ({expansion})", text, count=1)
    return text


def build_prompt(raw: RawObservation, glossary: dict[str, str] | None = None) -> tuple[str, str]:
    """Return (system, user) prompt strings for ``raw``.

    When ``glossary`` is provided, known abbreviations are inline-expanded in the
    input/output text the LLM sees.
    """
    glossary = glossary or {}
    tool_input = ""
    if raw.tool_input is not None:
        tool_input = json.dumps(raw.tool_input, sort_keys=True)[:1000]
    user = _USER_TEMPLATE.format(
        hook=raw.hook_type,
        tool=raw.tool_name or "",
        input=expand_abbreviations(clip(tool_input, 1000), glossary),
        output=expand_abbreviations(clip(raw.tool_output or "", 2000), glossary),
    )
    return _SYSTEM_PROMPT, user


def _strip_fences(text: str) -> str:
    """Strip markdown code fences that some models add despite instructions."""
    text = text.strip()
    text = re.sub(r"^```(?:json)?\s*\n?", "", text)
    text = re.sub(r"\n?```\s*$", "", text)
    return text.strip()


def parse_response(text: str, raw: RawObservation) -> CompressedObservation | None:
    """Parse the LLM JSON response into a CompressedObservation, or None on failure."""
    try:
        data = json.loads(_strip_fences(text))
    except (json.JSONDecodeError, ValueError):
        return None

    if not isinstance(data, dict):
        return None

    title = str(data.get("title") or raw.tool_name or raw.hook_type or "").strip()
    if not title:
        return None

    try:
        importance = int(data.get("importance", 5))
        importance = max(0, min(10, importance))
    except (TypeError, ValueError):
        importance = 5

    def _strlist(key: str) -> list[str]:
        val = data.get(key, [])
        if not isinstance(val, list):
            return []
        return [str(v).strip() for v in val if v and str(v).strip()]

    return CompressedObservation(
        id=raw.id,
        sessionId=raw.session_id,
        type=str(data.get("type") or "tool_use").strip(),
        title=clip(title, 120),
        subtitle=clip(str(data.get("subtitle") or ""), 200),
        facts=_strlist("facts"),
        narrative=clip(str(data.get("narrative") or ""), 1000),
        concepts=_strlist("concepts"),
        files=_strlist("files"),
        importance=importance,
        confidence=0.85,
        abbreviations=_parse_abbreviations(data.get("abbreviations")),
    )


def _parse_abbreviations(val: object) -> dict[str, str]:
    """Coerce the LLM's ``abbreviations`` field into a clean str→str map.

    Drops non-string/empty entries and caps the count so a runaway response
    can't flood the glossary merge. Validation of what counts as a real
    abbreviation happens authoritatively on the Go side (MergeGlossary)."""
    if not isinstance(val, dict):
        return {}
    out: dict[str, str] = {}
    for abbr, expansion in val.items():
        if len(out) >= _MAX_ABBREVIATIONS:
            break
        abbr_s = str(abbr).strip()
        exp_s = str(expansion).strip()
        if abbr_s and exp_s:
            out[abbr_s] = exp_s
    return out


class LLMCompressor:
    """LLM compressor with synthetic fallback on any error or absent client."""

    def __init__(
        self,
        client: LLMClient | None,
        settings: Settings,
        config: LLMCompressorConfig | None = None,
    ) -> None:
        self.client = client
        self.settings = settings
        self.config = config or LLMCompressorConfig()

    @property
    def name(self) -> str:
        return f"llm:{self.client.name}" if self.client else "synthetic-fallback"

    async def compress(
        self, raw: RawObservation, glossary: dict[str, str] | None = None
    ) -> CompressedObservation:
        """Compress with LLM if available, falling back to synthetic on any failure.

        ``glossary`` (abbreviation→expansion) inline-expands the text the LLM
        sees and is only used on the LLM path; the synthetic fallback ignores it.
        """
        if self.client is None:
            return synthetic_compress(raw)

        system, user = build_prompt(raw, glossary)
        try:
            text = await asyncio.wait_for(
                self.client.complete(
                    system, user, max_tokens=self.config.max_tokens, json_mode=True
                ),
                timeout=self.config.timeout_s,
            )
        except TimeoutError:
            logger.warning("llm_compress.timeout", observation_id=raw.id)
            return synthetic_compress(raw)
        except Exception as exc:  # noqa: BLE001 — fall back on any provider error
            logger.warning(
                "llm_compress.error",
                observation_id=raw.id,
                error=str(exc),
                provider=self.client.name,
            )
            return synthetic_compress(raw)

        parsed = parse_response(text, raw)
        if parsed is None:
            logger.warning(
                "llm_compress.parse_failed",
                observation_id=raw.id,
                response_preview=text[:200],
            )
            return synthetic_compress(raw)
        return parsed
