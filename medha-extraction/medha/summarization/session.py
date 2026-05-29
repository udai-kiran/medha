"""Session summarization with LLM-or-synthetic fallback."""

from __future__ import annotations

import asyncio
import logging
import re
from dataclasses import dataclass, field
from typing import Protocol

from pydantic import BaseModel, Field

from medha.compression.llm_compressor import LLMClient
from medha.config import Settings
from medha.utils.validators import clip

logger = logging.getLogger(__name__)


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
        # Take up to first sentence.
        m = re.match(r"([^.!?]{8,200})[.!?]?", n)
        if m:
            narrative_parts.append(m.group(1).strip())
    narrative = clip(" • ".join(narrative_parts) or "(no narratives)", 2000)

    seen_files: dict[str, None] = {}
    for d in digests:
        for f in d.files:
            seen_files.setdefault(f, None)
    files_modified = list(seen_files.keys())

    # Decisions: scan facts + narratives for "decided to ...", "use ..." etc.
    key_decisions: list[str] = []
    decision_re = re.compile(
        r"\b(?:decide[ds]?\s+to|use|chose|chosen|adopt(?:ed)?|prefer)\b[^.!?\n]{4,160}",
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


_SYSTEM_PROMPT = (
    "You summarise an agent coding session into structured XML for long-term memory.\n"
    "Highlight the goal, the key decisions made, and the files modified.\n"
    "Respond ONLY with the XML envelope."
)


def _build_user_prompt(digests: list[ObservationDigest]) -> str:
    lines = ["<observations>"]
    for d in digests[:50]:  # cap to keep prompt size reasonable
        lines.append(
            f"  <obs><title>{_xml_escape(d.title)}</title>"
            f"<narrative>{_xml_escape(clip(d.narrative, 200))}</narrative>"
            f"<files>{','.join(_xml_escape(f) for f in d.files[:5])}</files></obs>"
        )
    lines.append("</observations>")
    lines.append("")
    lines.append("Produce:")
    lines.append(
        "<summary>\n"
        "  <title>short session title</title>\n"
        "  <narrative>2-3 sentence overview</narrative>\n"
        "  <decisions><d>...</d></decisions>\n"
        "  <files><f>...</f></files>\n"
        "  <concepts><c>...</c></concepts>\n"
        "</summary>"
    )
    return "\n".join(lines)


def _xml_escape(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


_SUMMARY_RE = re.compile(r"<summary>(.*?)</summary>", re.DOTALL)
_TAG_RE = re.compile(r"<(\w+)>(.*?)</\1>", re.DOTALL)
_LIST_TAG_RE = re.compile(r"<(\w+)>(.*?)</\1>", re.DOTALL)


def _parse_llm(text: str, session_id: str) -> SessionSummary | None:
    m = _SUMMARY_RE.search(text)
    if not m:
        return None
    envelope = m.group(1)

    def _scalar(tag: str) -> str:
        sm = re.search(rf"<{tag}>(.*?)</{tag}>", envelope, re.DOTALL)
        return (sm.group(1) or "").strip() if sm else ""

    def _list(parent: str, item: str) -> list[str]:
        pm = re.search(rf"<{parent}>(.*?)</{parent}>", envelope, re.DOTALL)
        if not pm:
            return []
        inner = pm.group(1)
        return [m.group(1).strip() for m in re.finditer(rf"<{item}>(.*?)</{item}>", inner, re.DOTALL)]

    title = _scalar("title")
    narrative = _scalar("narrative")
    if not title and not narrative:
        return None
    return SessionSummary(
        sessionId=session_id,
        title=clip(title or "Session", 120),
        narrative=clip(narrative, 2000),
        keyDecisions=_list("decisions", "d"),
        filesModified=_list("files", "f"),
        concepts=_list("concepts", "c"),
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
    """LLM-or-synthetic session summarizer. Mirrors Task 13's LLMCompressor."""

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
                    _SYSTEM_PROMPT, _build_user_prompt(digests), max_tokens=self.config.max_tokens
                ),
                timeout=self.config.timeout_s,
            )
        except TimeoutError:
            logger.warning("summarize.timeout", extra={"session_id": session_id})
            return synthetic_session_summary(session_id, digests)
        except Exception as exc:  # noqa: BLE001
            logger.warning(
                "summarize.error", extra={"session_id": session_id, "error": str(exc)}
            )
            return synthetic_session_summary(session_id, digests)

        parsed = _parse_llm(text, session_id)
        if parsed is None:
            logger.warning(
                "summarize.parse_failed",
                extra={"session_id": session_id, "preview": text[:200]},
            )
            return synthetic_session_summary(session_id, digests)
        return parsed
