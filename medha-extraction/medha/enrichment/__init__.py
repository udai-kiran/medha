"""Entity enrichment (Task 30).

Sources:
  - Wikipedia REST summary API (free, rate-limited).
  - Wikidata (via Wikipedia's `pageprops`).
  - Diffbot (optional, key-gated).

The enricher is best-effort: failures fall through silently with the original
entity unchanged. Results cache locally (SQLite) so we respect upstream
rate limits across restarts.
"""

from medha.enrichment.cache import EnrichmentCache
from medha.enrichment.enricher import Enricher, EnrichmentResult
from medha.enrichment.wikipedia import WikipediaEnricher

__all__ = [
    "Enricher",
    "EnrichmentCache",
    "EnrichmentResult",
    "WikipediaEnricher",
]
