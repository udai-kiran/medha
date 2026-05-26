"""Tests for the enrichment cache + skip rules (Task 30)."""

from __future__ import annotations

from pathlib import Path

import pytest

from medha.enrichment import Enricher, EnrichmentCache, WikipediaEnricher


def test_cache_roundtrip(tmp_path: Path) -> None:
    c = EnrichmentCache(path=str(tmp_path / "cache.db"))
    c.put("k", {"name": "x", "desc": "y"})
    assert c.get("k") == {"name": "x", "desc": "y"}
    c.close()


def test_cache_ttl_expiry(tmp_path: Path) -> None:
    c = EnrichmentCache(path=str(tmp_path / "cache.db"), ttl_seconds=0)
    c.put("k", {"v": 1})
    assert c.get("k") is None  # already expired
    c.close()


@pytest.mark.asyncio()
async def test_enricher_skips_sensitive(tmp_path: Path) -> None:
    e = Enricher(
        cache=EnrichmentCache(path=str(tmp_path / "cache.db")),
        wikipedia=WikipediaEnricher(),
    )
    out = await e.enrich("Sensitive", sensitive=True)
    assert out is None


@pytest.mark.asyncio()
async def test_enricher_skips_empty_name(tmp_path: Path) -> None:
    e = Enricher(cache=EnrichmentCache(path=str(tmp_path / "cache.db")))
    assert await e.enrich("") is None


@pytest.mark.asyncio()
async def test_enricher_uses_cache(tmp_path: Path) -> None:
    """If the cache has the answer, the upstream is never contacted."""

    class _BoomWP(WikipediaEnricher):
        async def lookup(self, name: str):  # noqa: D401
            raise RuntimeError("must not be called when cached")

    cache = EnrichmentCache(path=str(tmp_path / "cache.db"))
    cache.put("wp:PERSON:alice", {"name": "Alice"})
    e = Enricher(cache=cache, wikipedia=_BoomWP())
    res = await e.enrich("Alice", entity_type="PERSON")
    assert res is not None
    assert res.cached is True
    assert res.fields == {"name": "Alice"}
