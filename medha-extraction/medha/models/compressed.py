"""CompressedObservation — output of the compression pipeline (Task 11/13)."""

from __future__ import annotations

from pydantic import BaseModel, Field


class CompressedObservation(BaseModel):
    """The compact, searchable form of an observation.

    Mirrors `go/internal/models/observation.go` `CompressedObservation`.
    """

    id: str = Field(..., description="Same id as the originating RawObservation")
    session_id: str = Field(..., alias="sessionId")
    type: str = Field(..., description="file_read | file_edit | command | search | ...")
    title: str
    subtitle: str = ""
    facts: list[str] = Field(default_factory=list)
    narrative: str = ""
    concepts: list[str] = Field(default_factory=list)
    files: list[str] = Field(default_factory=list)
    importance: int = Field(default=5, ge=0, le=10)
    confidence: float = Field(default=0.3, ge=0.0, le=1.0)
    image_description: str | None = Field(default=None, alias="imageDescription")

    model_config = {"populate_by_name": True}
