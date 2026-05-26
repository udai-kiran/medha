"""Embedding providers (Task 15).

The local provider is a deterministic hashing embedding so the service works
out of the box with no API key. Real providers (OpenAI, Gemini, Voyage,
Xenova all-MiniLM) layer on top via the same `Embedder` interface.
"""

from medha.embedding.local_embedder import LocalEmbedder
from medha.embedding.providers import (
    Embedder,
    EmbeddingResult,
    PROVIDER_DIMENSIONS,
    pick_embedder,
)

__all__ = [
    "Embedder",
    "EmbeddingResult",
    "LocalEmbedder",
    "PROVIDER_DIMENSIONS",
    "pick_embedder",
]
