"""Relationship schema — typed edges between entities."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

RelationshipType = Literal[
    "DEPENDS_ON",
    "IMPLEMENTS",
    "WORKS_AT",
    "RELATED_TO",
    "CONTRADICTS",
    "SUPERSEDES",
    "DERIVED_FROM",
]


class Relationship(BaseModel):
    """A directed, typed edge between two entities with confidence + provenance."""

    source: str = Field(..., description="Source entity name")
    target: str = Field(..., description="Target entity name")
    type: RelationshipType
    confidence: float = Field(default=0.5, ge=0.0, le=1.0)
    source_observation_id: str | None = Field(default=None, alias="sourceObservationId")

    model_config = {"populate_by_name": True}
