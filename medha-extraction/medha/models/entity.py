"""Entity schema (POLE+O — Person/Object/Location/Event + Organization)."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field

EntityType = Literal["PERSON", "OBJECT", "LOCATION", "EVENT", "ORGANIZATION"]


class Entity(BaseModel):
    """A typed entity extracted from one or more observations."""

    name: str
    type: EntityType
    subtype: str | None = None
    confidence: float = Field(default=0.5, ge=0.0, le=1.0)
    source_observation_ids: list[str] = Field(default_factory=list, alias="sourceObservationIds")
    aliases: list[str] = Field(default_factory=list)

    model_config = {"populate_by_name": True}
