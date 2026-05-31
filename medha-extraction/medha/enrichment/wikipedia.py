"""Wikipedia REST summary lookup.

Uses the public ``/api/rest_v1/page/summary/{title}`` endpoint, which serves
short descriptions, Wikidata IDs, and thumbnail URLs at low quota cost.
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import Any

import httpx
import structlog

logger = structlog.get_logger(__name__)


@dataclass
class WikipediaEnricher:
    """Thin async wrapper around the Wikipedia summary API."""

    rate_limit_per_sec: float = 0.5
    timeout_s: float = 10.0
    base_url: str = "https://en.wikipedia.org/api/rest_v1/page/summary"

    def __post_init__(self) -> None:
        self._last_call = 0.0
        self._lock = asyncio.Lock()

    async def lookup(self, name: str) -> dict[str, Any] | None:
        """Return enrichment fields for ``name`` or None on miss/error."""
        if not name:
            return None
        await self._wait()

        try:
            async with httpx.AsyncClient(timeout=self.timeout_s) as client:
                resp = await client.get(
                    f"{self.base_url}/{name.replace(' ', '_')}",
                    headers={"User-Agent": "agent_mem/0.1 (https://github.com/oneconvergence/agent-mem)"},
                )
        except Exception as exc:  # noqa: BLE001
            logger.warning("wikipedia.lookup_failed", name=name, error=str(exc))
            return None

        if resp.status_code != 200:
            return None
        body = resp.json()
        if body.get("type") == "disambiguation":
            return None

        thumbnail = body.get("thumbnail", {}) or {}
        wikidata_id = body.get("wikibase_item")

        return {
            "name": body.get("title"),
            "description": body.get("description"),
            "summary": body.get("extract"),
            "wikipedia_url": (body.get("content_urls", {}) or {}).get("desktop", {}).get("page"),
            "wikidata_id": wikidata_id,
            "image_url": thumbnail.get("source"),
        }

    async def _wait(self) -> None:
        async with self._lock:
            min_gap = 1.0 / max(self.rate_limit_per_sec, 0.01)
            now = asyncio.get_event_loop().time()
            elapsed = now - self._last_call
            if elapsed < min_gap:
                await asyncio.sleep(min_gap - elapsed)
            self._last_call = asyncio.get_event_loop().time()
