"""LLM-driven compression with synthetic fallback (FR-12).

The flow:
  1. Build a system+user prompt from the RawObservation.
  2. Call the configured LLM with a timeout (60s default).
  3. Parse an XML-flavoured response into a CompressedObservation.
  4. On any failure (timeout, parse error, no API key), fall back to the
     synthetic path so the pipeline never blocks.

The XML format mirrors agent_mem.md §"Phase 1B" so the parser stays simple.
We accept slightly malformed XML (e.g. missing closing tags on optional
fields) because LLMs occasionally truncate — fall through to synthetic
rather than reject in that case.
"""

from __future__ import annotations

import asyncio
import logging
import re
import xml.etree.ElementTree as ET
from dataclasses import dataclass
from typing import Protocol

from medha.compression.synthetic_compressor import synthetic_compress
from medha.config import Settings
from medha.models import CompressedObservation, RawObservation
from medha.utils.validators import clip

logger = logging.getLogger(__name__)


class LLMClient(Protocol):
    """Narrow surface that any provider client (Anthropic, OpenAI, …) must satisfy."""

    async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str: ...

    @property
    def name(self) -> str: ...


@dataclass(frozen=True)
class LLMCompressorConfig:
    """Per-call knobs for the LLM compressor."""

    timeout_s: float = 60.0
    max_tokens: int = 1024


_SYSTEM_PROMPT = (
    "You compress agent observations into structured XML for long-term memory.\n"
    "Extract facts, narrative, concepts, files, and an importance (0-10).\n"
    "Respond ONLY with the XML envelope. No prose before or after."
)

_USER_TEMPLATE = (
    "<observation>\n"
    "  <hook>{hook}</hook>\n"
    "  <tool>{tool}</tool>\n"
    "  <input>{input}</input>\n"
    "  <output>{output}</output>\n"
    "</observation>\n\n"
    "Produce:\n"
    "<compressed>\n"
    "  <type>file_read|file_edit|command|search|...</type>\n"
    "  <title>short title</title>\n"
    "  <subtitle>optional</subtitle>\n"
    "  <facts><fact>...</fact><fact>...</fact></facts>\n"
    "  <narrative>1-2 sentence summary</narrative>\n"
    "  <concepts><concept>...</concept></concepts>\n"
    "  <files><file>...</file></files>\n"
    "  <importance>0-10</importance>\n"
    "</compressed>"
)


def build_prompt(raw: RawObservation) -> tuple[str, str]:
    """Return (system, user) prompt strings for ``raw``."""
    tool_input = ""
    if raw.tool_input is not None:
        import json as _json

        tool_input = _json.dumps(raw.tool_input, sort_keys=True)[:1000]
    user = _USER_TEMPLATE.format(
        hook=raw.hook_type,
        tool=raw.tool_name or "",
        input=clip(tool_input, 1000),
        output=clip(raw.tool_output or "", 2000),
    )
    return _SYSTEM_PROMPT, user


# Robust regex-based parser: ET is strict, and LLMs are not. We use ET when
# the response is well-formed and fall through to regex on the optional inner
# arrays. Top-level <compressed> failure is treated as a parse failure.
_FACT_RE = re.compile(r"<fact>\s*(.*?)\s*</fact>", re.DOTALL)
_CONCEPT_RE = re.compile(r"<concept>\s*(.*?)\s*</concept>", re.DOTALL)
_FILE_RE = re.compile(r"<file>\s*(.*?)\s*</file>", re.DOTALL)


def parse_response(text: str, raw: RawObservation) -> CompressedObservation | None:
    """Parse the LLM response into a CompressedObservation, or None on failure."""
    # Find the <compressed>...</compressed> envelope; LLMs sometimes wrap it.
    m = re.search(r"<compressed>.*?</compressed>", text, re.DOTALL)
    if not m:
        return None
    envelope = m.group(0)

    # Try ET for the well-formed case.
    try:
        root = ET.fromstring(envelope)
    except ET.ParseError:
        # Fall back to regex extraction of the scalar fields.
        return _parse_lenient(envelope, raw)

    def _text(tag: str, default: str = "") -> str:
        el = root.find(tag)
        return (el.text or default).strip() if el is not None else default

    def _list(tag: str, child: str) -> list[str]:
        parent = root.find(tag)
        if parent is None:
            return []
        return [(e.text or "").strip() for e in parent.findall(child) if (e.text or "").strip()]

    try:
        importance = int(_text("importance", "5") or 5)
    except ValueError:
        importance = 5

    return CompressedObservation(
        id=raw.id,
        sessionId=raw.session_id,
        type=_text("type") or "tool_use",
        title=clip(_text("title") or (raw.tool_name or raw.hook_type), 120),
        subtitle=clip(_text("subtitle"), 200),
        facts=_list("facts", "fact"),
        narrative=clip(_text("narrative"), 1000),
        concepts=_list("concepts", "concept"),
        files=_list("files", "file"),
        importance=importance,
        confidence=0.85,
    )


def _parse_lenient(envelope: str, raw: RawObservation) -> CompressedObservation | None:
    """Best-effort regex parse for slightly malformed XML."""

    def _scalar(tag: str) -> str:
        m = re.search(rf"<{tag}>(.*?)</{tag}>", envelope, re.DOTALL)
        return m.group(1).strip() if m else ""

    typ = _scalar("type") or "tool_use"
    title = _scalar("title") or (raw.tool_name or raw.hook_type)
    if not title:
        return None
    try:
        importance = int(_scalar("importance") or "5")
    except ValueError:
        importance = 5

    return CompressedObservation(
        id=raw.id,
        sessionId=raw.session_id,
        type=typ,
        title=clip(title, 120),
        subtitle=clip(_scalar("subtitle"), 200),
        facts=_FACT_RE.findall(envelope),
        narrative=clip(_scalar("narrative"), 1000),
        concepts=_CONCEPT_RE.findall(envelope),
        files=_FILE_RE.findall(envelope),
        importance=importance,
        confidence=0.7,  # slightly lower because parse was lenient
    )


class LLMCompressor:
    """LLM compressor with synthetic fallback on any error or absent client.

    Construct with a concrete LLMClient (Task 19 wires Anthropic/OpenAI/Gemini)
    or ``None`` to always use the synthetic fallback (M2 default).
    """

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

    async def compress(self, raw: RawObservation) -> CompressedObservation:
        """Compress with LLM if available, falling back to synthetic on any failure."""
        if self.client is None or not self.settings.has_any_llm():
            return synthetic_compress(raw)

        system, user = build_prompt(raw)
        try:
            text = await asyncio.wait_for(
                self.client.complete(system, user, max_tokens=self.config.max_tokens),
                timeout=self.config.timeout_s,
            )
        except TimeoutError:
            logger.warning("llm_compress.timeout", extra={"observation_id": raw.id})
            return synthetic_compress(raw)
        except Exception as exc:  # noqa: BLE001 — fall back on any provider error
            logger.warning(
                "llm_compress.error",
                extra={"observation_id": raw.id, "error": str(exc), "provider": self.client.name},
            )
            return synthetic_compress(raw)

        parsed = parse_response(text, raw)
        if parsed is None:
            logger.warning(
                "llm_compress.parse_failed",
                extra={"observation_id": raw.id, "response_preview": text[:200]},
            )
            return synthetic_compress(raw)
        return parsed
