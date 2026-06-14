"""OpenAI-compatible embedding client (covers OpenAI native and OpenRouter)."""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any

import httpx
import structlog

from medha.embedding.providers import EmbeddingResult

logger = structlog.get_logger(__name__)


class EmbeddingProviderError(RuntimeError):
    """Raised when an embedding provider returns an unusable response."""


@dataclass
class OpenAIEmbedder:
    """Async embedder for any OpenAI-compatible /embeddings endpoint."""

    base_url: str
    api_key: str
    model: str
    _dimension: int

    @property
    def provider(self) -> str:
        host = self.base_url.split("//", 1)[-1].split("/")[0]
        return f"{host}/{self.model}"

    @property
    def dimension(self) -> int:
        return self._dimension

    async def embed(self, texts: list[str]) -> EmbeddingResult:
        headers: dict[str, str] = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        if "openrouter" in self.base_url:
            headers["HTTP-Referer"] = "https://github.com/udai-kiran/medha"
            headers["X-Title"] = "medha"

        vectors = await self._embed_with_retry(headers, texts)
        actual_dim = len(vectors[0]) if vectors else self._dimension
        return EmbeddingResult(vectors=vectors, provider=self.provider, dimension=actual_dim)

    async def _embed_with_retry(
        self, headers: dict[str, str], texts: list[str]
    ) -> list[list[float]]:
        last_exc: Exception | None = None
        for attempt in range(1, 3):
            try:
                return await self._embed_once(headers, texts)
            except (EmbeddingProviderError, httpx.HTTPError, ValueError) as exc:
                last_exc = exc
                if attempt == 2:
                    break
                logger.warning(
                    "embedding.provider_retry",
                    provider=self.provider,
                    attempt=attempt,
                    err=str(exc),
                )
                await asyncio.sleep(0.2 * attempt)

        raise EmbeddingProviderError(
            "embedding provider returned an unusable response"
        ) from last_exc

    async def _embed_once(self, headers: dict[str, str], texts: list[str]) -> list[list[float]]:
        async with httpx.AsyncClient(timeout=6.0) as client:
            resp = await client.post(
                f"{self.base_url}/embeddings",
                headers=headers,
                json={"model": self.model, "input": texts},
            )
            resp.raise_for_status()
            data: dict[str, Any] = resp.json()

        return _vectors_from_response(data)


def _vectors_from_response(data: dict[str, Any]) -> list[list[float]]:
    items = data.get("data")
    if not isinstance(items, list):
        raise EmbeddingProviderError(
            "embedding response missing data array: " + _response_summary(data)
        )

    vectors: list[list[float]] = []
    for item in sorted(items, key=_embedding_index):
        if not isinstance(item, dict):
            raise EmbeddingProviderError("embedding response item is not an object")
        embedding = item.get("embedding")
        if not isinstance(embedding, list):
            raise EmbeddingProviderError("embedding response item missing embedding array")
        vectors.append(embedding)
    return vectors


def _embedding_index(item: object) -> int:
    if not isinstance(item, dict):
        return 0
    index = item.get("index", 0)
    return index if isinstance(index, int) else 0


def _response_summary(data: dict[str, Any]) -> str:
    summary = {
        "keys": sorted(data.keys()),
        "error": data.get("error"),
        "message": data.get("message"),
        "detail": data.get("detail"),
    }
    return repr(summary)[:300]
