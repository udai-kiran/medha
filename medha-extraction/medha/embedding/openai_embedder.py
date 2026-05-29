"""OpenAI-compatible embedding client (covers OpenAI native and OpenRouter)."""

from __future__ import annotations

import logging
from dataclasses import dataclass

import httpx

from medha.embedding.providers import EmbeddingResult

logger = logging.getLogger(__name__)


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

        async with httpx.AsyncClient(timeout=30.0) as client:
            resp = await client.post(
                f"{self.base_url}/embeddings",
                headers=headers,
                json={"model": self.model, "input": texts},
            )
            resp.raise_for_status()
            data: dict = resp.json()

        vectors = [
            item["embedding"]
            for item in sorted(data["data"], key=lambda x: x["index"])
        ]
        actual_dim = len(vectors[0]) if vectors else self._dimension
        return EmbeddingResult(vectors=vectors, provider=self.provider, dimension=actual_dim)
