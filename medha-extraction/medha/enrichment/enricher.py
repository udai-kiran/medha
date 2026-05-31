"""High-level enricher: cache + Wikipedia + (future) Diffbot.

Skips enrichment for entities flagged as sensitive (FR-9) — those are
secrets-adjacent and should never leave the host.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

import structlog

from medha.enrichment.cache import EnrichmentCache
from medha.enrichment.wikipedia import WikipediaEnricher

logger = structlog.get_logger(__name__)


@dataclass
class EnrichmentResult:
    """What the enricher returns. ``source`` identifies the upstream."""

    fields: dict[str, Any] = field(default_factory=dict)
    source: str = "wikipedia"
    cached: bool = False


@dataclass
class Enricher:
    """Compose cache + provider(s). Used by Task 30 inside the consolidation
    pipeline (background, rate-limited)."""

    cache: EnrichmentCache | None = None
    wikipedia: WikipediaEnricher | None = None

    def __post_init__(self) -> None:
        if self.wikipedia is None:
            self.wikipedia = WikipediaEnricher()

    async def enrich(
        self, name: str, *, entity_type: str | None = None, sensitive: bool = False
    ) -> EnrichmentResult | None:
        if not name or sensitive:
            return None

        # Cache key includes the entity type so "Apple" the company and
        # "Apple" the fruit don't collide.
        cache_key = f"wp:{entity_type or ''}:{name.lower()}"

        if self.cache is not None:
            cached = self.cache.get(cache_key)
            if cached is not None:
                return EnrichmentResult(fields=cached, source="cache", cached=True)

        wp = self.wikipedia
        if wp is None:
            return None
        out = await wp.lookup(name)
        if out is None:
            return None

        if self.cache is not None:
            self.cache.put(cache_key, out)

        return EnrichmentResult(fields=out, source="wikipedia", cached=False)
