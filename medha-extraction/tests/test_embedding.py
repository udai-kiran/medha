"""Tests for the local embedder + /embed endpoint (Task 15)."""

from __future__ import annotations

import math

import pytest
from fastapi.testclient import TestClient

from medha.embedding import LocalEmbedder
from medha.embedding.openai_embedder import EmbeddingProviderError, OpenAIEmbedder


@pytest.mark.asyncio()
async def test_local_embedder_deterministic() -> None:
    e = LocalEmbedder(dimension=128)
    r1 = await e.embed(["hello world"])
    r2 = await e.embed(["hello world"])
    assert r1.vectors == r2.vectors
    assert r1.provider == "local"
    assert r1.dimension == 128


@pytest.mark.asyncio()
async def test_local_embedder_l2_normalised() -> None:
    e = LocalEmbedder(dimension=64)
    r = await e.embed(["JWT authentication"])
    norm = math.sqrt(sum(x * x for x in r.vectors[0]))
    assert math.isclose(norm, 1.0, abs_tol=1e-6)


@pytest.mark.asyncio()
async def test_local_embedder_similar_texts_correlate() -> None:
    e = LocalEmbedder(dimension=384)
    r = await e.embed(
        ["JWT authentication token", "authentication token JWT", "unrelated yak shaving"]
    )
    v1, v2, v3 = r.vectors
    sim12 = sum(a * b for a, b in zip(v1, v2, strict=True))
    sim13 = sum(a * b for a, b in zip(v1, v3, strict=True))
    # Same tokens reordered → identical vectors (hashing-trick is order-free).
    assert math.isclose(sim12, 1.0, abs_tol=1e-6)
    assert sim13 < sim12


def test_embed_endpoint(client: TestClient) -> None:
    r = client.post("/embed", json={"texts": ["hello", "world"]})
    assert r.status_code == 200
    body = r.json()
    assert body["provider"]  # non-empty, varies by configuration
    assert body["dimension"] > 0
    assert len(body["embeddings"]) == 2
    assert len(body["embeddings"][0]) == body["dimension"]


class _FakeEmbeddingResponse:
    def __init__(self, body: dict[str, object]) -> None:
        self._body = body

    def raise_for_status(self) -> None:
        return None

    def json(self) -> dict[str, object]:
        return self._body


class _FakeAsyncClient:
    responses: list[dict[str, object]] = []
    calls = 0

    def __init__(self, timeout: float) -> None:
        self.timeout = timeout

    async def __aenter__(self) -> "_FakeAsyncClient":
        return self

    async def __aexit__(self, exc_type: object, exc: object, tb: object) -> None:
        return None

    async def post(self, *args: object, **kwargs: object) -> _FakeEmbeddingResponse:
        body = self.responses[min(self.calls, len(self.responses) - 1)]
        self.__class__.calls += 1
        return _FakeEmbeddingResponse(body)


async def _no_sleep(delay: float) -> None:
    return None


@pytest.mark.asyncio()
async def test_openai_embedder_retries_invalid_data(monkeypatch: pytest.MonkeyPatch) -> None:
    _FakeAsyncClient.responses = [
        {"object": "list", "data": None, "error": {"message": "temporary upstream miss"}},
        {
            "object": "list",
            "data": [
                {"index": 1, "embedding": [0.0, 1.0]},
                {"index": 0, "embedding": [1.0, 0.0]},
            ],
        },
    ]
    _FakeAsyncClient.calls = 0
    monkeypatch.setattr("medha.embedding.openai_embedder.httpx.AsyncClient", _FakeAsyncClient)
    monkeypatch.setattr("medha.embedding.openai_embedder.asyncio.sleep", _no_sleep)

    embedder = OpenAIEmbedder(
        base_url="http://bifrost/v1",
        api_key="",
        model="openrouter/qwen/qwen3-embedding-8b",
        _dimension=2,
    )

    result = await embedder.embed(["hello", "world"])

    assert _FakeAsyncClient.calls == 2
    assert result.vectors == [[1.0, 0.0], [0.0, 1.0]]
    assert result.dimension == 2


@pytest.mark.asyncio()
async def test_openai_embedder_invalid_data_raises_provider_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _FakeAsyncClient.responses = [
        {"object": "list", "data": None, "error": {"message": "upstream failed"}},
    ]
    _FakeAsyncClient.calls = 0
    monkeypatch.setattr("medha.embedding.openai_embedder.httpx.AsyncClient", _FakeAsyncClient)
    monkeypatch.setattr("medha.embedding.openai_embedder.asyncio.sleep", _no_sleep)

    embedder = OpenAIEmbedder(
        base_url="http://bifrost/v1",
        api_key="",
        model="openrouter/qwen/qwen3-embedding-8b",
        _dimension=2,
    )

    with pytest.raises(EmbeddingProviderError, match="unusable response"):
        await embedder.embed(["hello"])

    assert _FakeAsyncClient.calls == 2
