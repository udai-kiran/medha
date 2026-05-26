"""Embedder protocol + provider selection."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol

from medha.config import Settings


@dataclass(frozen=True)
class EmbeddingResult:
    """One batch of embeddings + the provider that produced them."""

    vectors: list[list[float]]
    provider: str
    dimension: int


class Embedder(Protocol):
    """Pluggable embedding backend."""

    @property
    def provider(self) -> str: ...

    @property
    def dimension(self) -> int: ...

    async def embed(self, texts: list[str]) -> EmbeddingResult: ...


# Default output dimensions per provider. The local provider is fixed at 384
# (matches Xenova all-MiniLM-L6-v2) so projects can switch from local to
# Xenova without a re-index.
PROVIDER_DIMENSIONS: dict[str, int] = {
    "local": 384,
    "openai": 1536,
    "gemini": 768,
    "voyage": 1024,
}


def pick_embedder(settings: Settings) -> Embedder:
    """Pick an Embedder based on settings; the local one is always available."""
    # Real providers land alongside Task 19's LLM clients; for M2 we route
    # everything through LocalEmbedder so the pipeline runs without network.
    from medha.embedding.local_embedder import LocalEmbedder

    return LocalEmbedder(dimension=PROVIDER_DIMENSIONS.get(settings.embedding_provider, 384))
