"""Embedding providers.

The local provider is a deterministic hashing embedding so the service works
out of the box with no API key. The bifrost provider routes through the
Bifrost gateway using the OpenAI-compatible embeddings endpoint.
"""

from medha.embedding.local_embedder import LocalEmbedder
from medha.embedding.openai_embedder import OpenAIEmbedder
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
    "OpenAIEmbedder",
    "PROVIDER_DIMENSIONS",
    "pick_embedder",
]
