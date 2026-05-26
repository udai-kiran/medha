"""Multi-stage extraction pipeline + merger.

The pipeline chains Extractors and merges their outputs by (name, type):
when two stages produce the same entity, we keep the higher-confidence
record and union the source observation ids.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass, field
from typing import Protocol

from medha.extraction.heuristic_extractor import HeuristicExtractor
from medha.models import Entity


class Extractor(Protocol):
    """Stage in the extraction pipeline."""

    @property
    def name(self) -> str: ...

    def __call__(self, text: str, *, source_observation_id: str | None = None) -> list[Entity]: ...


@dataclass
class ExtractionResult:
    """Pipeline output."""

    entities: list[Entity] = field(default_factory=list)
    stages_run: list[str] = field(default_factory=list)


@dataclass
class ExtractionPipeline:
    """Run extractors in order, merge results."""

    extractors: list[Extractor]

    def extract(self, text: str, *, source_observation_id: str | None = None) -> ExtractionResult:
        result = ExtractionResult()
        for stage in self.extractors:
            ents = stage(text, source_observation_id=source_observation_id)
            result.stages_run.append(stage.name)
            result.entities = _merge(result.entities, ents)
        return result


def _merge(base: list[Entity], new: Iterable[Entity]) -> list[Entity]:
    """Merge two entity lists by (name.lower(), type) — keep highest confidence."""
    index: dict[tuple[str, str], int] = {(e.name.lower(), e.type): i for i, e in enumerate(base)}
    out = list(base)
    for e in new:
        key = (e.name.lower(), e.type)
        if key in index:
            existing = out[index[key]]
            if e.confidence > existing.confidence:
                merged = e.model_copy(
                    update={
                        "sourceObservationIds": _union_ids(
                            existing.source_observation_ids, e.source_observation_ids
                        )
                    }
                )
            else:
                merged = existing.model_copy(
                    update={
                        "sourceObservationIds": _union_ids(
                            existing.source_observation_ids, e.source_observation_ids
                        )
                    }
                )
            out[index[key]] = merged
        else:
            index[key] = len(out)
            out.append(e)
    return out


def _union_ids(a: list[str], b: list[str]) -> list[str]:
    seen: dict[str, None] = {}
    for x in (*a, *b):
        if x:
            seen[x] = None
    return list(seen.keys())


def default_pipeline() -> ExtractionPipeline:
    """The skeleton pipeline: heuristic only.

    Task 27 adds spaCy / GLiNER / LLM stages by appending to ``extractors``.
    """
    return ExtractionPipeline(extractors=[HeuristicExtractor()])
