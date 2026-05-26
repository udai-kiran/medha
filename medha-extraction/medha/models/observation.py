"""RawObservation schema — what the Go service hands the Python service."""

from __future__ import annotations

from datetime import datetime
from typing import Any, Literal

from pydantic import BaseModel, Field

Modality = Literal["text", "image", "mixed"]


class RawObservation(BaseModel):
    """A single agent observation before compression/extraction.

    Mirrors `go/internal/models/observation.go` `RawObservation`.
    """

    id: str = Field(..., description="obs-... identifier")
    session_id: str = Field(..., alias="sessionId")
    project: str | None = None
    timestamp: datetime
    hook_type: str = Field(..., alias="hookType")
    tool_name: str | None = Field(default=None, alias="toolName")
    tool_input: dict[str, Any] | None = Field(default=None, alias="toolInput")
    tool_output: str | None = Field(default=None, alias="toolOutput")
    user_prompt: str | None = Field(default=None, alias="userPrompt")
    raw: dict[str, Any] = Field(default_factory=dict)
    modality: Modality = "text"
    image_data: str | None = Field(default=None, alias="imageData", description="data:image/... payload")
    has_secrets: bool = Field(default=False, alias="hasSecrets")

    model_config = {"populate_by_name": True}
