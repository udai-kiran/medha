"""LLM-driven entity + relationship extraction with heuristic fallback.

This is the primary extraction path. The flow mirrors ``LLMCompressor``:

  1. Build a system+user prompt from the observation text.
  2. Call the configured LLM with json_mode=True.
  3. Parse the JSON into typed ``Entity`` / ``Relationship`` records, dropping
     anything outside the fixed POLE+O / relationship type sets.
  4. On any failure (no client, timeout, parse error) fall back to the
     heuristic pipeline so the service is never network-dependent (NFR-9).

The LLM path is *exclusive*: when it succeeds we return its entities and
relationships only — we do not merge the noisy regex heuristics back in. The
heuristic pipeline runs solely as the offline / failure floor.
"""

from __future__ import annotations

import asyncio
import json
import re
from dataclasses import dataclass, field

import structlog

from medha.config import Settings
from medha.extraction.pipeline import default_pipeline
from medha.extraction.relationships import extract_relationships
from medha.extraction.types import classify_subtype
from medha.llm.client import LLMClient
from medha.models import Entity, Relationship

logger = structlog.get_logger(__name__)

# Mirror the model literals so we can reject anything off-list before Pydantic
# would raise. Keep these in lockstep with medha.models.entity / .relationship.
_ENTITY_TYPES = {"PERSON", "OBJECT", "LOCATION", "EVENT", "ORGANIZATION"}
_RELATIONSHIP_TYPES = {
    "DEPENDS_ON",
    "IMPLEMENTS",
    "WORKS_AT",
    "RELATED_TO",
    "CONTRADICTS",
    "SUPERSEDES",
    "DERIVED_FROM",
}

# Cap per response so a runaway model can't flood the graph from one observation.
_MAX_ENTITIES = 40
_MAX_RELATIONSHIPS = 40


@dataclass(frozen=True)
class LLMExtractorConfig:
    """Per-call knobs for the LLM extractor."""

    timeout_s: float = 60.0
    # Higher than compression's 1024: a dense observation yields many entities,
    # and a truncated JSON body parses as failure → heuristic fallback → exactly
    # the regex noise this stage exists to remove.
    max_tokens: int = 2048


@dataclass
class ExtractionOutcome:
    """What the extractor returns: entities, relationships, and stages run."""

    entities: list[Entity] = field(default_factory=list)
    relationships: list[Relationship] = field(default_factory=list)
    stages_run: list[str] = field(default_factory=list)


_SYSTEM_PROMPT = (
    "You extract a knowledge graph from a software agent's observation text.\n"
    "Return MEANINGFUL, named entities only — the things a developer would want "
    "to recall later: people, organizations, tools, libraries, services, "
    "systems, protocols, files, and distinct technical concepts.\n"
    "Do NOT emit raw code symbols, local variable names, function/identifier "
    "tokens, generic words, pronouns, or title-cased sentence fragments "
    "(e.g. 'The Go', 'This Python', 'Running Tests'). When unsure, omit it.\n"
    "Every entity 'type' MUST be one of: PERSON, OBJECT, LOCATION, EVENT, "
    "ORGANIZATION. Map tools/libraries/services/files/concepts to OBJECT and "
    "put the finer kind in 'subtype' (e.g. LIBRARY, SERVICE, FILE, CONCEPT).\n"
    "Every relationship 'type' MUST be one of: DEPENDS_ON, IMPLEMENTS, "
    "WORKS_AT, RELATED_TO, CONTRADICTS, SUPERSEDES, DERIVED_FROM. Use a "
    "relationship's 'source' and 'target' EXACTLY as they appear in your "
    "entities list — never introduce a name you did not also list as an entity.\n"
    "Respond ONLY with a JSON object. No prose, no markdown fences."
)

_USER_TEMPLATE = """\
Observation text:
{text}

Produce a JSON object with exactly these keys:
{{
  "entities": [
    {{"name": "exact name", "type": "OBJECT", "subtype": "LIBRARY", "confidence": 0.0-1.0}}
  ],
  "relationships": [
    {{"source": "entity name", "target": "entity name", "type": "DEPENDS_ON", "confidence": 0.9}}
  ]
}}"""


def build_extract_prompt(text: str) -> tuple[str, str]:
    """Return (system, user) prompt strings for ``text``."""
    return _SYSTEM_PROMPT, _USER_TEMPLATE.format(text=text)


def _strip_fences(text: str) -> str:
    """Strip markdown code fences that some models add despite instructions."""
    text = text.strip()
    text = re.sub(r"^```(?:json)?\s*\n?", "", text)
    text = re.sub(r"\n?```\s*$", "", text)
    return text.strip()


def _clamp_confidence(val: object, default: float) -> float:
    try:
        return max(0.0, min(1.0, float(val)))  # type: ignore[arg-type]
    except (TypeError, ValueError):
        return default


def parse_extract_response(
    text: str, source_observation_id: str | None
) -> tuple[list[Entity], list[Relationship]] | None:
    """Parse the LLM JSON into typed entities + relationships, or None on failure.

    Entities/relationships whose type falls outside the fixed literal sets are
    dropped (not coerced) so off-list guesses never reach the graph.
    """
    try:
        data = json.loads(_strip_fences(text))
    except (json.JSONDecodeError, ValueError):
        return None
    if not isinstance(data, dict):
        return None

    obs_ids = [source_observation_id] if source_observation_id else []

    entities: list[Entity] = []
    seen_entities: set[tuple[str, str]] = set()
    raw_entities = data.get("entities")
    if isinstance(raw_entities, list):
        for item in raw_entities:
            if len(entities) >= _MAX_ENTITIES:
                break
            if not isinstance(item, dict):
                continue
            name = str(item.get("name") or "").strip()
            etype = str(item.get("type") or "").strip().upper()
            if not name or etype not in _ENTITY_TYPES:
                continue
            key = (name.lower(), etype)
            if key in seen_entities:
                continue
            seen_entities.add(key)
            subtype = str(item.get("subtype") or "").strip() or None
            if subtype is None:
                subtype = classify_subtype(name, etype)
            entities.append(
                Entity(
                    name=name,
                    type=etype,  # type: ignore[arg-type]  # validated against _ENTITY_TYPES
                    subtype=subtype,
                    confidence=_clamp_confidence(item.get("confidence"), 0.75),
                    sourceObservationIds=obs_ids,
                )
            )

    relationships: list[Relationship] = []
    raw_rels = data.get("relationships")
    if isinstance(raw_rels, list):
        for item in raw_rels:
            if len(relationships) >= _MAX_RELATIONSHIPS:
                break
            if not isinstance(item, dict):
                continue
            source = str(item.get("source") or "").strip()
            target = str(item.get("target") or "").strip()
            rtype = str(item.get("type") or "").strip().upper()
            if not source or not target or rtype not in _RELATIONSHIP_TYPES:
                continue
            relationships.append(
                Relationship(
                    source=source,
                    target=target,
                    type=rtype,  # type: ignore[arg-type]  # validated against _RELATIONSHIP_TYPES
                    confidence=_clamp_confidence(item.get("confidence"), 0.75),
                    sourceObservationId=source_observation_id,
                )
            )

    return entities, relationships


class LLMExtractor:
    """LLM entity/relationship extractor with heuristic fallback.

    When no client is configured, or the LLM call/parse fails, falls back to the
    regex heuristic pipeline so ``/extract`` always returns something usable.
    """

    def __init__(
        self,
        client: LLMClient | None,
        settings: Settings,
        config: LLMExtractorConfig | None = None,
    ) -> None:
        self.client = client
        self.settings = settings
        self.config = config or LLMExtractorConfig()
        self._heuristic = default_pipeline()

    @property
    def name(self) -> str:
        return f"llm:{self.client.name}" if self.client else "heuristic"

    def _heuristic_outcome(
        self, text: str, source_observation_id: str | None
    ) -> ExtractionOutcome:
        result = self._heuristic.extract(text, source_observation_id=source_observation_id)
        rels = extract_relationships(text, source_observation_id=source_observation_id)
        return ExtractionOutcome(
            entities=result.entities,
            relationships=rels,
            stages_run=result.stages_run,
        )

    async def extract(
        self, text: str, source_observation_id: str | None = None
    ) -> ExtractionOutcome:
        """Extract via LLM, falling back to heuristics on any failure."""
        if not text:
            return ExtractionOutcome(stages_run=["empty"])
        if self.client is None:
            return self._heuristic_outcome(text, source_observation_id)

        system, user = build_extract_prompt(text)
        try:
            raw = await asyncio.wait_for(
                self.client.complete(
                    system, user, max_tokens=self.config.max_tokens, json_mode=True
                ),
                timeout=self.config.timeout_s,
            )
        except TimeoutError:
            logger.warning("llm_extract.timeout", observation_id=source_observation_id)
            return self._heuristic_outcome(text, source_observation_id)
        except Exception as exc:  # noqa: BLE001 — fall back on any provider error
            logger.warning(
                "llm_extract.error",
                observation_id=source_observation_id,
                error=str(exc),
                provider=self.client.name,
            )
            return self._heuristic_outcome(text, source_observation_id)

        parsed = parse_extract_response(raw, source_observation_id)
        if parsed is None:
            logger.warning(
                "llm_extract.parse_failed",
                observation_id=source_observation_id,
                response_preview=raw[:200],
            )
            return self._heuristic_outcome(text, source_observation_id)

        entities, relationships = parsed
        return ExtractionOutcome(
            entities=entities,
            relationships=relationships,
            stages_run=[f"llm:{self.client.name}"],
        )
