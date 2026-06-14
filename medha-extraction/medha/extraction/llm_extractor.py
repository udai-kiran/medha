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


_SYSTEM_PROMPT = """\
You extract a knowledge graph from a software agent's observation text.
The graph feeds entity search and cross-session intelligence — extract durable,
meaningful nodes only. Prefer precision over recall: a missed entity is harmless;
a noisy one pollutes every future search that touches it.

ENTITIES — named things a developer would want to recall across sessions:
  tools, libraries, frameworks, services, databases, APIs, protocols, languages,
  design patterns, and central file artefacts (config files, schema files, core
  source files). NOT: local variables, temp files, one-off paths, generic class
  names, code tokens, pronouns, or title-cased sentence fragments like "The Go".
  Use the canonical official name: "PostgreSQL" not "postgres" or "Postgres db";
  "GitHub Actions" not "GH Actions". When uncertain whether something qualifies, omit it.
  Max 15 entities per observation.

ENTITY TYPE — must be one of: PERSON · OBJECT · LOCATION · EVENT · ORGANIZATION
  Nearly all software entities are OBJECT. Use subtype to be specific — must be
  one of this closed set:
    TOOL · LIBRARY · FRAMEWORK · SERVICE · DATABASE · API · PROTOCOL ·
    LANGUAGE · FILE · CONCEPT · PATTERN · ORGANIZATION_UNIT
  PERSON and ORGANIZATION: actual humans and companies only, not software components.
  LOCATION and EVENT: rarely applicable in code observations — omit rather than force-fit.

ENTITY CONFIDENCE — how certain is this entity meaningful in this observation?
  0.9+   named explicitly and central to what happened
  0.7–0.8 present but peripheral, or name inferred rather than stated
  < 0.7  omit

RELATIONSHIPS — structural or semantic connections only. "Both appear in the same
  observation" is not a relationship. Each type has a fixed direction (source → target):
    DEPENDS_ON   source requires target to function (imports, calls, extends)
    IMPLEMENTS   source is a concrete implementation of target interface or spec
    SUPERSEDES   source is the newer replacement for target
    DERIVED_FROM source was built from or extends target
    CONTRADICTS  source conflicts with, replaces, or invalidates target
    WORKS_AT     PERSON works at ORGANIZATION
    RELATED_TO   genuinely connected but no more specific type fits — use sparingly;
                 never as a default; if you are unsure of the type, omit the relationship
  source and target must be exact names from your entities list — never introduce
  a name you did not also list as an entity.
  Max 20 relationships per observation. Omit weak or speculative links.

RELATIONSHIP CONFIDENCE:
  0.9+   explicitly stated in the text
  0.7–0.8 strongly implied by context
  < 0.7  omit

Respond ONLY with a JSON object. No prose, no markdown fences."""

_USER_TEMPLATE = """\
Observation text:
{text}

Produce a JSON object with exactly these keys:
{{
  "entities": [
    {{"name": "canonical name", "type": "OBJECT", "subtype": "LIBRARY", "confidence": 0.9}}
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
