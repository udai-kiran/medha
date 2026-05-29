"""FastAPI entrypoint for the Python service.

Bound to ``:5000``. Routes here are intentionally minimal in M0; later tasks
register routers under the same app:

  - Task 11 → ``POST /compress``
  - Task 15 → ``POST /embed``
  - Task 19 → ``POST /extract``
  - Task 21 → ``POST /summarize``
  - Task 30 → ``POST /enrich``
"""

from __future__ import annotations

import logging
from contextlib import asynccontextmanager
from typing import Any

from fastapi import FastAPI
from fastapi.responses import PlainTextResponse
from prometheus_client import CONTENT_TYPE_LATEST, CollectorRegistry, Counter, generate_latest

from pydantic import BaseModel, Field

from medha import __version__
from medha.compression import LLMCompressor, LLMCompressorConfig, SyntheticCompressor, validate_compressed
from medha.config import Settings, get_settings
from medha.embedding import pick_embedder
from medha.enrichment import EnrichmentCache, Enricher, WikipediaEnricher
from medha.extraction import default_pipeline, extract_relationships
from medha.llm import build_llm_client
from medha.models import CompressedObservation, Entity, RawObservation, Relationship
from medha.summarization import ObservationDigest, synthetic_session_summary
from medha.summarization.session import SessionSummary, SessionSummarizer, SessionSummarizerConfig
from medha.utils.logging import configure_logging

logger = logging.getLogger(__name__)


# Per-app Prometheus registry so tests don't leak metrics across runs.
metrics_registry = CollectorRegistry()
requests_total = Counter(
    "agent_mem_py_requests_total",
    "HTTP requests handled by the Python service",
    labelnames=("route", "status"),
    registry=metrics_registry,
)


@asynccontextmanager
async def lifespan(app: FastAPI) -> Any:
    """Wire settings, LLM clients, and logging at startup."""
    settings = get_settings()
    configure_logging(settings.log_level)
    app.state.settings = settings

    # Build per-stage LLM clients (None → synthetic fallback, no crash).
    compress_client = build_llm_client(settings.resolve_stage_model("compress"), settings)
    summarize_client = build_llm_client(settings.resolve_stage_model("summarize"), settings)

    app.state.compressor = LLMCompressor(
        client=compress_client,
        settings=settings,
        config=LLMCompressorConfig(),
    )
    app.state.summarizer = SessionSummarizer(
        client=summarize_client,
        settings=settings,
        config=SessionSummarizerConfig(),
    )

    stage_map = {
        "compress": compress_client.name if compress_client else "synthetic",
        "summarize": summarize_client.name if summarize_client else "synthetic",
        "extract": settings.resolve_stage_model("extract") or "heuristic",
        "embed": f"bifrost:{settings.embedding_fingerprint()}" if settings.embedding_model else "local",
    }
    logger.info("py.startup", extra={"version": __version__, "stages": stage_map})
    yield
    logger.info("py.shutdown")


app = FastAPI(
    title="medha (Python)",
    version=__version__,
    description="Extraction, compression, summarization, embeddings, enrichment.",
    lifespan=lifespan,
)


@app.get("/health")
async def health() -> dict[str, Any]:
    """Liveness probe — always returns 200 unless the process is dying."""
    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    compressor: LLMCompressor | None = getattr(app.state, "compressor", None)
    summarizer: SessionSummarizer | None = getattr(app.state, "summarizer", None)
    requests_total.labels(route="/health", status="200").inc()
    return {
        "status": "ok",
        "version": __version__,
        "stages": {
            "compress": compressor.name if compressor else "synthetic",
            "summarize": summarizer.name if summarizer else "synthetic",
            "extract": settings.resolve_stage_model("extract") or "heuristic",
            "embed": f"bifrost:{settings.embedding_fingerprint()}" if settings.embedding_model else "local",
        },
    }


@app.get("/metrics", response_class=PlainTextResponse)
async def metrics() -> PlainTextResponse:
    """Prometheus exposition endpoint."""
    return PlainTextResponse(
        generate_latest(metrics_registry).decode("utf-8"),
        media_type=CONTENT_TYPE_LATEST,
    )


# Single shared compressor instance — synthetic path is stateless, so reuse
# is cheap and avoids per-request allocation. Task 13 wraps this in an LLM
# compressor with the synthetic path as fallback.
_synthetic = SyntheticCompressor()


class EmbedRequest(BaseModel):
    """Body for POST /embed."""

    texts: list[str]


class EmbedResponse(BaseModel):
    """Body of a successful /embed call."""

    embeddings: list[list[float]]
    provider: str
    dimension: int


@app.post("/embed", response_model=EmbedResponse)
async def embed(req: EmbedRequest) -> EmbedResponse:
    """Embed a batch of texts using the configured provider.

    The local provider needs no key and is the default; OpenAI/Gemini/Voyage
    join in Task 19 alongside the LLM clients.
    """
    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    embedder = pick_embedder(settings)
    result = await embedder.embed(req.texts)
    requests_total.labels(route="/embed", status="200").inc()
    return EmbedResponse(
        embeddings=result.vectors, provider=result.provider, dimension=result.dimension
    )


class ExtractRequest(BaseModel):
    """Body for POST /extract."""

    text: str
    source_observation_id: str | None = None


class ExtractResponse(BaseModel):
    """Body of a successful /extract call."""

    entities: list[Entity]
    relationships: list[Relationship]
    stages_run: list[str]


# Single shared pipeline; default = heuristic only (NFR-9 no-network floor).
_extraction_pipeline = default_pipeline()


@app.post("/extract", response_model=ExtractResponse)
async def extract(req: ExtractRequest) -> ExtractResponse:
    """Extract typed entities + relationships from text.

    The heuristic pipeline catches file paths, identifiers, URLs, and
    typical verb-driven relationships. Real spaCy/GLiNER/LLM extractors
    layer on top via the same interface.
    """
    result = _extraction_pipeline.extract(
        req.text, source_observation_id=req.source_observation_id
    )
    relationships = extract_relationships(req.text, source_observation_id=req.source_observation_id)
    requests_total.labels(route="/extract", status="200").inc()
    return ExtractResponse(
        entities=result.entities,
        relationships=relationships,
        stages_run=result.stages_run,
    )


class ObservationDigestModel(BaseModel):
    """Wire shape of an observation digest passed to /summarize."""

    title: str
    narrative: str = ""
    concepts: list[str] = Field(default_factory=list)
    files: list[str] = Field(default_factory=list)
    facts: list[str] = Field(default_factory=list)


class SummarizeRequest(BaseModel):
    """Body for POST /summarize."""

    session_id: str = Field(..., alias="sessionId")
    observations: list[ObservationDigestModel] = Field(default_factory=list)

    model_config = {"populate_by_name": True}


@app.post("/summarize", response_model=SessionSummary)
async def summarize(req: SummarizeRequest) -> SessionSummary:
    """Summarise a session from its observation digests via LLM (or synthetic fallback)."""
    digests = [
        ObservationDigest(
            title=o.title,
            narrative=o.narrative,
            concepts=o.concepts,
            files=o.files,
            facts=o.facts,
        )
        for o in req.observations
    ]
    summarizer: SessionSummarizer = getattr(app.state, "summarizer", None) or SessionSummarizer(
        client=None,
        settings=app.state.settings if hasattr(app.state, "settings") else get_settings(),
    )
    requests_total.labels(route="/summarize", status="200").inc()
    return await summarizer.summarize(req.session_id, digests)


class EnrichRequest(BaseModel):
    """Body for POST /enrich."""

    name: str
    entity_type: str | None = Field(default=None, alias="entityType")
    sensitive: bool = False

    model_config = {"populate_by_name": True}


class EnrichResponse(BaseModel):
    """Body of a successful /enrich call. ``fields`` is None on miss."""

    fields: dict[str, Any] | None = None
    source: str = "none"
    cached: bool = False


# Shared enricher — wraps the cache and Wikipedia provider.
_enricher: Enricher | None = None


def _get_enricher(settings: Settings) -> Enricher:
    global _enricher
    if _enricher is None:
        cache = EnrichmentCache(path="data/enrichment_cache.db")
        _enricher = Enricher(
            cache=cache,
            wikipedia=WikipediaEnricher(rate_limit_per_sec=settings.wikipedia_rate_limit),
        )
    return _enricher


@app.post("/enrich", response_model=EnrichResponse)
async def enrich(req: EnrichRequest) -> EnrichResponse:
    """Enrich an entity from external knowledge sources (Wikipedia)."""
    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    e = _get_enricher(settings)
    res = await e.enrich(req.name, entity_type=req.entity_type, sensitive=req.sensitive)
    requests_total.labels(route="/enrich", status="200").inc()
    if res is None:
        return EnrichResponse(fields=None, source="none", cached=False)
    return EnrichResponse(fields=res.fields, source=res.source, cached=res.cached)


@app.post("/compress", response_model=CompressedObservation)
async def compress(raw: RawObservation) -> CompressedObservation:
    """Compress a single RawObservation via LLM (or synthetic fallback)."""
    compressor: LLMCompressor = getattr(app.state, "compressor", None) or LLMCompressor(
        client=None,
        settings=app.state.settings if hasattr(app.state, "settings") else get_settings(),
    )
    result = await compressor.compress(raw)
    requests_total.labels(route="/compress", status="200").inc()
    return validate_compressed(result)


class TitleRequest(BaseModel):
    content: str = Field(..., description="Memory content to generate a title for")


class TitleResponse(BaseModel):
    title: str
    generated: bool = Field(description="True if LLM was used, False if fallback")


@app.post("/title", response_model=TitleResponse)
async def generate_title(req: TitleRequest) -> TitleResponse:
    """Generate a short title for memory content via LLM, with word-truncation fallback."""
    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    client = build_llm_client(settings.resolve_stage_model("compress"), settings)
    if client is not None:
        try:
            prompt = (
                "Generate a short, descriptive title (5–8 words) for the following memory. "
                "Return only the title text, no punctuation at the end, no quotes.\n\n"
                f"{req.content[:800]}"
            )
            import asyncio
            response = await asyncio.wait_for(
                client.complete(system="You are a concise title generator.", user=prompt),
                timeout=15.0,
            )
            title = response.strip().strip('"').strip("'")
            if title:
                requests_total.labels(route="/title", status="200").inc()
                return TitleResponse(title=title[:80], generated=True)
        except Exception:
            pass
    # Fallback: first 8 words
    words = req.content.split()[:8]
    title = " ".join(words)
    if len(title) > 60:
        title = title[:57] + "..."
    requests_total.labels(route="/title", status="200").inc()
    return TitleResponse(title=title, generated=False)

