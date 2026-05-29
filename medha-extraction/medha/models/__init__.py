"""Pydantic models mirroring the Go domain types in `medha-api/internal/models`.

Field shapes must round-trip with the Go JSON tags — when a Go struct gains
a field, update its Python twin in lockstep so the HTTP boundary stays typed.
"""

from medha.models.compressed import CompressedObservation
from medha.models.entity import Entity
from medha.models.observation import RawObservation
from medha.models.relationship import Relationship

__all__ = ["CompressedObservation", "Entity", "RawObservation", "Relationship"]
