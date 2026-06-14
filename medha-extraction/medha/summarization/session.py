"""Session summarization with LLM-or-synthetic fallback."""

from __future__ import annotations

import asyncio
import json
import re
from dataclasses import dataclass, field
from typing import Protocol

import structlog
from pydantic import BaseModel, Field

from medha.compression.llm_compressor import LLMClient
from medha.config import Settings
from medha.utils.validators import clip

logger = structlog.get_logger(__name__)


@dataclass
class ObservationDigest:
    """The compressed-observation slice the summarizer reads.

    Keeps the summarizer decoupled from the full CompressedObservation Pydantic
    type — callers can build digests from any source (storage row, in-memory
    state, etc.).
    """

    title: str
    narrative: str = ""
    concepts: list[str] = field(default_factory=list)
    files: list[str] = field(default_factory=list)
    facts: list[str] = field(default_factory=list)


class SessionSummary(BaseModel):
    """The output shape — mirrors Go's models.SessionSummary."""

    session_id: str = Field(..., alias="sessionId")
    title: str
    narrative: str
    key_decisions: list[str] = Field(default_factory=list, alias="keyDecisions")
    files_modified: list[str] = Field(default_factory=list, alias="filesModified")
    concepts: list[str] = Field(default_factory=list)

    model_config = {"populate_by_name": True}


# --- Synthetic path -----------------------------------------------------------


def synthetic_session_summary(
    session_id: str, digests: list[ObservationDigest]
) -> SessionSummary:
    """Build a session summary from observation digests without an LLM.

    Strategy:
      - Title: count common concepts; pick the most frequent + "session".
      - Narrative: concatenate the first sentence of each narrative, capped.
      - Key decisions: lines starting with "decide(d) ...", "use ..." in narratives.
      - Files modified: union of all digests.files.
      - Concepts: union of all digests.concepts, ranked by frequency, top 10.
    """
    if not digests:
        return SessionSummary(
            sessionId=session_id,
            title="Empty session",
            narrative="No observations.",
        )

    concept_counts: dict[str, int] = {}
    for d in digests:
        for c in d.concepts:
            key = c.lower().strip()
            if key:
                concept_counts[key] = concept_counts.get(key, 0) + 1
    top_concepts = sorted(concept_counts.items(), key=lambda x: -x[1])
    top_concepts_list = [c for c, _ in top_concepts[:10]]

    title_lead = top_concepts_list[0] if top_concepts_list else digests[0].title
    title = clip(f"Session on {title_lead}".strip(), 120)

    narrative_parts: list[str] = []
    for d in digests[:20]:  # cap to avoid runaway concatenation
        n = d.narrative.strip()
        if not n:
            continue
        m = re.match(r"([^.!?]{8,200})[.!?]?", n)
        if m:
            narrative_parts.append(m.group(1).strip())
    narrative = clip(" • ".join(narrative_parts) or "(no narratives)", 2000)

    seen_files: dict[str, None] = {}
    for d in digests:
        for f in d.files:
            seen_files.setdefault(f, None)
    files_modified = list(seen_files.keys())

    key_decisions: list[str] = []
    decision_re = re.compile(
        r"\b(?:decide[ds]?\s+to|use|chose|chosen|adopt(?:ed)?|prefer)\b[^.!?\n]{15,160}",
        re.IGNORECASE,
    )
    seen_decisions: set[str] = set()
    for d in digests:
        for source in (d.facts, [d.narrative]):
            for txt in source:
                if not txt:
                    continue
                for m in decision_re.finditer(txt):
                    decision = m.group(0).strip(" .,;:")
                    norm = decision.lower()
                    if norm in seen_decisions:
                        continue
                    seen_decisions.add(norm)
                    key_decisions.append(decision)
        if len(key_decisions) >= 10:
            break

    return SessionSummary(
        sessionId=session_id,
        title=title,
        narrative=narrative,
        keyDecisions=key_decisions[:10],
        filesModified=files_modified,
        concepts=top_concepts_list,
    )


# --- LLM path -----------------------------------------------------------------


_SYSTEM_PROMPT = """\
You summarise a software agent's coding session into structured JSON for long-term memory.
A future agent will query this to recall what was attempted, what was decided, and what
changed — write for retrieval, not for a human reader.

TITLE — a present-tense gerund phrase naming the primary activity, max 100 chars.
  Good: "Implementing JWT auth in Express middleware"
        "Debugging Postgres connection pool exhaustion"
        "Refactoring the payment service to use Stripe SDK v3"
  Bad:  "Session"  "Coding session"  "Various tasks"  "Working on the project"

NARRATIVE — exactly 2-3 sentences in this order:
  1. What the session set out to do (the goal or trigger).
  2. What was accomplished or discovered (concrete outcomes, not "worked on X").
  3. What remains open or was deferred — omit this sentence if the task completed cleanly.
  Name the key files, libraries, error types, or commands involved. Be specific.

DECISIONS — concrete choices that constrain or shape future work. Include:
  library/tool selections, architectural patterns adopted, bug-fix strategies chosen,
  API contracts agreed, schemas changed, approaches explicitly rejected (and why).
  Each entry must be self-contained: "Chose jose over jsonwebtoken because it supports
  ES256 natively" not "chose a JWT library". Leave empty if no substantive choices were made.

FILES — only paths that appear verbatim in the observation data. Never infer or reconstruct.

CONCEPTS — searchable technical terms that index this session: library names, tool names,
  error types, protocols, design patterns, domain concepts. NOT generic nouns (file, code,
  function, session, output). Max 10, ordered by relevance.

Respond ONLY with a JSON object. No prose, no markdown fences."""

_USER_TEMPLATE = """\
<observations>
{obs_block}
</observations>

Produce a JSON object with exactly these keys:
{{
  "title": "present-tense gerund phrase, max 100 chars",
  "narrative": "2-3 sentences: goal / outcome / open work",
  "decisions": ["self-contained choice that shapes future work", ...],
  "files": ["verbatim/path/from/observations", ...],
  "concepts": ["searchable technical term", ...]
}}"""


def _build_user_prompt(digests: list[ObservationDigest]) -> str:
    lines = []
    for d in digests[:50]:  # cap to keep prompt size reasonable
        lines.append(
            f'  {{"title": {json.dumps(d.title)}, '
            f'"narrative": {json.dumps(clip(d.narrative, 200))}, '
            f'"facts": {json.dumps(d.facts[:4])}, '
            f'"files": {json.dumps(d.files[:5])}}}'
        )
    return _USER_TEMPLATE.format(obs_block="\n".join(lines))


def _strip_fences(text: str) -> str:
    text = text.strip()
    text = re.sub(r"^```(?:json)?\s*\n?", "", text)
    text = re.sub(r"\n?```\s*$", "", text)
    return text.strip()


def _parse_llm(text: str, session_id: str) -> SessionSummary | None:
    try:
        data = json.loads(_strip_fences(text))
    except (json.JSONDecodeError, ValueError):
        return None

    if not isinstance(data, dict):
        return None

    title = str(data.get("title") or "").strip()
    narrative = str(data.get("narrative") or "").strip()
    if not title and not narrative:
        return None

    def _strlist(key: str) -> list[str]:
        val = data.get(key, [])
        if not isinstance(val, list):
            return []
        return [str(v).strip() for v in val if v and str(v).strip()]

    return SessionSummary(
        sessionId=session_id,
        title=clip(title or "Session", 120),
        narrative=clip(narrative, 2000),
        keyDecisions=_strlist("decisions"),
        filesModified=_strlist("files"),
        concepts=_strlist("concepts"),
    )


class SyntheticSessionSummarizer:
    """Stateless wrapper around `synthetic_session_summary`."""

    name = "synthetic"

    def summarize(self, session_id: str, digests: list[ObservationDigest]) -> SessionSummary:
        return synthetic_session_summary(session_id, digests)


class _LLMSummarizerProtocol(Protocol):
    async def summarize(
        self, session_id: str, digests: list[ObservationDigest]
    ) -> SessionSummary: ...


@dataclass(frozen=True)
class SessionSummarizerConfig:
    timeout_s: float = 60.0
    max_tokens: int = 1024


class SessionSummarizer:
    """LLM-or-synthetic session summarizer."""

    def __init__(
        self,
        client: LLMClient | None,
        settings: Settings,
        config: SessionSummarizerConfig | None = None,
    ) -> None:
        self.client = client
        self.settings = settings
        self.config = config or SessionSummarizerConfig()

    @property
    def name(self) -> str:
        return f"llm:{self.client.name}" if self.client else "synthetic-fallback"

    async def summarize(
        self, session_id: str, digests: list[ObservationDigest]
    ) -> SessionSummary:
        if self.client is None:
            return synthetic_session_summary(session_id, digests)
        try:
            text = await asyncio.wait_for(
                self.client.complete(
                    _SYSTEM_PROMPT,
                    _build_user_prompt(digests),
                    max_tokens=self.config.max_tokens,
                    json_mode=True,
                ),
                timeout=self.config.timeout_s,
            )
        except TimeoutError:
            logger.warning("summarize.timeout", session_id=session_id)
            return synthetic_session_summary(session_id, digests)
        except Exception as exc:  # noqa: BLE001
            logger.warning("summarize.error", session_id=session_id, error=str(exc))
            return synthetic_session_summary(session_id, digests)

        parsed = _parse_llm(text, session_id)
        if parsed is None:
            logger.warning(
                "summarize.parse_failed",
                session_id=session_id,
                preview=text[:200],
            )
            return synthetic_session_summary(session_id, digests)
        return parsed
