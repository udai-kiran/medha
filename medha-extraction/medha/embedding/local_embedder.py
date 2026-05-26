"""Local, deterministic hashing embedder.

Properties:
  - Deterministic: same input → same vector across runs.
  - L2-normalised: cosine similarity is straightforward (dot product).
  - Zero external dependencies.

This is intentionally not a "good" embedder — it's the offline-friendly
floor (NFR-9). Task 19 wires in Xenova/all-MiniLM-L6-v2 for the same
dimension, allowing a transparent upgrade without reindexing.
"""

from __future__ import annotations

import hashlib
import math
import re
from collections.abc import Iterable

from medha.embedding.providers import Embedder, EmbeddingResult


_TOKEN_RE = re.compile(r"[A-Za-z0-9]+")


def _tokens(text: str) -> Iterable[str]:
    for m in _TOKEN_RE.finditer(text.lower()):
        yield m.group(0)


def _hash_token(tok: str, dim: int) -> tuple[int, int]:
    """Return (slot, sign) for ``tok``.

    Sign trick (random ±1) keeps the embedding zero-mean in expectation.
    """
    h = hashlib.blake2b(tok.encode("utf-8"), digest_size=8).digest()
    slot = int.from_bytes(h[:4], "little") % dim
    sign = 1 if h[4] & 1 else -1
    return slot, sign


class LocalEmbedder(Embedder):
    """Hashing trick embedder (HashingVectorizer-style + L2 norm)."""

    def __init__(self, dimension: int = 384) -> None:
        if dimension <= 0:
            raise ValueError("dimension must be positive")
        self._dim = dimension

    @property
    def provider(self) -> str:
        return "local"

    @property
    def dimension(self) -> int:
        return self._dim

    def _embed_one(self, text: str) -> list[float]:
        vec = [0.0] * self._dim
        for tok in _tokens(text):
            slot, sign = _hash_token(tok, self._dim)
            vec[slot] += float(sign)
        # L2 normalise (skip for the all-zero vector to avoid division by zero).
        norm = math.sqrt(sum(x * x for x in vec))
        if norm > 0:
            inv = 1.0 / norm
            vec = [x * inv for x in vec]
        return vec

    async def embed(self, texts: list[str]) -> EmbeddingResult:
        vectors = [self._embed_one(t) for t in texts]
        return EmbeddingResult(vectors=vectors, provider=self.provider, dimension=self._dim)
