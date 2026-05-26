"""Tests for the local embedder + /embed endpoint (Task 15)."""

from __future__ import annotations

import math

import pytest
from fastapi.testclient import TestClient

from medha.embedding import LocalEmbedder


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
    r = await e.embed(["JWT authentication token", "authentication token JWT", "unrelated yak shaving"])
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
    assert body["provider"] == "local"
    assert body["dimension"] == 384
    assert len(body["embeddings"]) == 2
    assert len(body["embeddings"][0]) == 384
