"""Entity extraction pipeline (Task 19).

Multi-stage: fast heuristics first (regex + capitalisation patterns) then
optional spaCy/GLiNER/LLM passes. The skeleton ships only the heuristic
extractor so the service is functional without heavy NLP installs.

Real spaCy/GLiNER extractors satisfy the same `Extractor` protocol — Task 27
can drop them in once the runtime dependencies are pinned.
"""

from medha.extraction.heuristic_extractor import HeuristicExtractor
from medha.extraction.pipeline import (
    Extractor,
    ExtractionPipeline,
    ExtractionResult,
    default_pipeline,
)
from medha.extraction.relationships import (
    HeuristicRelationshipExtractor,
    co_occurrence_relationships,
    extract_relationships,
)
from medha.extraction.types import POLE_O_TYPES, classify_subtype

__all__ = [
    "Extractor",
    "ExtractionPipeline",
    "ExtractionResult",
    "HeuristicExtractor",
    "HeuristicRelationshipExtractor",
    "POLE_O_TYPES",
    "classify_subtype",
    "co_occurrence_relationships",
    "default_pipeline",
    "extract_relationships",
]
