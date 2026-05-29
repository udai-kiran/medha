"""Embedder protocol + provider selection."""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass
from typing import Protocol

from medha.config import Settings

logger = logging.getLogger(__name__)


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


# Known output dimensions per model. Bare model names (no vendor prefix).
PROVIDER_DIMENSIONS: dict[str, int] = {
    "local": 384,
    # OpenAI-compatible models accessible via Bifrost
    "text-embedding-3-small": 1536,
    "text-embedding-3-large": 3072,
    "text-embedding-ada-002": 1536,
}


def _dim_for_model(model: str) -> int:
    """Return embedding dimension for a model name (strip vendor prefix first)."""
    bare = model.split("/", 1)[1] if "/" in model else model
    return PROVIDER_DIMENSIONS.get(bare) or PROVIDER_DIMENSIONS.get(model, 1536)


def _check_fingerprint(settings: Settings) -> None:
    """Warn if the embedding model changed since the index was last built."""
    fp_path = os.path.join("data", "embedding_fingerprint")
    current = settings.embedding_fingerprint()
    try:
        if os.path.exists(fp_path):
            with open(fp_path) as _f:
                stored = _f.read().strip()
            if stored and stored != current:
                logger.warning(
                    "embedding.model_changed — vector index may be stale; "
                    "reindex required. previous=%s current=%s",
                    stored,
                    current,
                )
        else:
            os.makedirs("data", exist_ok=True)
            with open(fp_path, "w") as f:
                f.write(current)
    except OSError:
        pass


def pick_embedder(settings: Settings) -> Embedder:
    """Return the configured Embedder.

    Uses Bifrost when EMBEDDING_MODEL is set; falls back to the local
    hashing embedder otherwise.
    """
    _check_fingerprint(settings)

    if not settings.embedding_model:
        from medha.embedding.local_embedder import LocalEmbedder
        return LocalEmbedder(dimension=PROVIDER_DIMENSIONS["local"])

    # Bifrost wraps OpenRouter: openrouter/<vendor>/<model>
    model = settings.embedding_model
    bifrost_model = model if model.startswith("openrouter/") else (
        f"openrouter/{model}" if "/" in model else f"openrouter/openai/{model}"
    )
    from medha.embedding.openai_embedder import OpenAIEmbedder
    return OpenAIEmbedder(
        base_url=f"{settings.bifrost_url}/v1",
        api_key=settings.bifrost_api_key,
        model=bifrost_model,
        _dimension=_dim_for_model(model),
    )
