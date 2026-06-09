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

from contextlib import asynccontextmanager
from typing import Any

import structlog
from fastapi import FastAPI
from fastapi.responses import PlainTextResponse
from prometheus_client import CONTENT_TYPE_LATEST, CollectorRegistry, Counter, generate_latest
from pydantic import BaseModel, Field

from medha import __version__
from medha.compression import (
    LLMCompressor,
    LLMCompressorConfig,
    SyntheticCompressor,
    validate_compressed,
)
from medha.config import Settings, get_settings
from medha.embedding import pick_embedder
from medha.enrichment import Enricher, EnrichmentCache, WikipediaEnricher
from medha.extraction import LLMExtractor, LLMExtractorConfig
from medha.llm import build_llm_client
from medha.models import CompressedObservation, Entity, RawObservation, Relationship
from medha.summarization import ObservationDigest
from medha.summarization.session import SessionSummarizer, SessionSummarizerConfig, SessionSummary
from medha.utils.logging import configure_logging

logger = structlog.get_logger(__name__)


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
    extract_client = build_llm_client(settings.resolve_stage_model("extract"), settings)

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
    app.state.extractor = LLMExtractor(
        client=extract_client,
        settings=settings,
        config=LLMExtractorConfig(),
    )

    stage_map = {
        "compress": compress_client.name if compress_client else "synthetic",
        "summarize": summarize_client.name if summarize_client else "synthetic",
        "extract": app.state.extractor.name,
        "embed": (
            f"bifrost:{settings.embedding_fingerprint()}" if settings.embedding_model else "local"
        ),
    }
    logger.info("py.startup", version=__version__, stages=stage_map)
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
    extractor: LLMExtractor | None = getattr(app.state, "extractor", None)
    requests_total.labels(route="/health", status="200").inc()
    return {
        "status": "ok",
        "version": __version__,
        "stages": {
            "compress": compressor.name if compressor else "synthetic",
            "summarize": summarizer.name if summarizer else "synthetic",
            "extract": extractor.name if extractor else "heuristic",
            "embed": (
                f"bifrost:{settings.embedding_fingerprint()}"
                if settings.embedding_model
                else "local"
            ),
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


@app.post("/extract", response_model=ExtractResponse)
async def extract(req: ExtractRequest) -> ExtractResponse:
    """Extract typed entities + relationships from text.

    The LLM extractor is the primary path — it selects meaningful, named
    entities rather than scraping every code symbol. The regex heuristic
    pipeline runs only as the offline / failure fallback (NFR-9 no-network
    floor), so this endpoint always returns something usable.
    """
    extractor: LLMExtractor = app.state.extractor
    outcome = await extractor.extract(
        req.text, source_observation_id=req.source_observation_id
    )
    requests_total.labels(route="/extract", status="200").inc()
    return ExtractResponse(
        entities=outcome.entities,
        relationships=outcome.relationships,
        stages_run=outcome.stages_run,
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


_EXTRACT_MEMORY_SYSTEM_PROMPT = """You extract structure from a user's natural \
language memory note for a software engineering agent.

Given the raw note, produce:
- type: one of architecture|pattern|preference|bug|workflow|fact
- normalized_content: restate the note as a clear, complete sentence or two
- concepts: 3-5 lowercase technical terms or proper nouns (tool names, language features, patterns)
- facts: any specific concrete assertions (can be empty)

Type guidance:
- architecture: design decisions, component structure, technology choices
- pattern: recurring code or design patterns observed
- preference: user/team coding style or tool preferences
- bug: identified defects and their root causes
- workflow: process steps, commands, development flows
- fact: any other concrete piece of information

Return JSON only. Example:
{"type":"architecture",\
"normalized_content":"JWT validation uses the jose library in src/auth.ts middleware.",\
"concepts":["jwt","jose","middleware","authentication"],\
"facts":["jose library handles token validation"]}"""


class ExtractMemoryRequest(BaseModel):
    """Body for POST /extract-memory."""

    content: str


class ExtractedMemory(BaseModel):
    type: str
    normalized_content: str
    concepts: list[str] = Field(default_factory=list)
    facts: list[str] = Field(default_factory=list)


_VALID_MEMORY_TYPES = {"architecture", "pattern", "preference", "bug", "workflow", "fact"}


@app.post("/extract-memory", response_model=ExtractedMemory)
async def extract_memory_nlq(req: ExtractMemoryRequest) -> ExtractedMemory:
    """Extract structured memory fields from a natural language note via LLM.

    Falls back to type=fact with the content as-is when LLM is unavailable.
    """
    import asyncio
    import json as _json
    import re as _re

    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    client = build_llm_client(settings.resolve_stage_model("compress"), settings)

    if client is not None:
        try:
            response = await asyncio.wait_for(
                client.complete(
                    system=_EXTRACT_MEMORY_SYSTEM_PROMPT,
                    user=f"Note: {req.content}",
                ),
                timeout=15.0,
            )
            text = response.strip()
            if "```" in text:
                text = _re.sub(r"```(?:json)?\n?", "", text).strip().rstrip("`")
            data = _json.loads(text)
            mem_type = data.get("type", "fact")
            if mem_type not in _VALID_MEMORY_TYPES:
                mem_type = "fact"
            normalized = data.get("normalized_content", req.content).strip() or req.content
            concepts = [str(c).lower().strip() for c in data.get("concepts", []) if c][:6]
            facts = [str(f).strip() for f in data.get("facts", []) if f][:10]
            requests_total.labels(route="/extract-memory", status="200").inc()
            return ExtractedMemory(
                type=mem_type,
                normalized_content=normalized,
                concepts=concepts,
                facts=facts,
            )
        except Exception:
            logger.debug("extract-memory LLM failed, using fallback", exc_info=True)

    # Synthetic fallback: guess type as fact, extract words as concepts.
    words = [w.lower() for w in _re.findall(r"\b[a-zA-Z][a-zA-Z0-9_.-]{2,}\b", req.content)]
    seen: dict[str, None] = {}
    for w in words:
        seen.setdefault(w, None)
    concepts = list(seen.keys())[:5]
    requests_total.labels(route="/extract-memory", status="200").inc()
    return ExtractedMemory(type="fact", normalized_content=req.content, concepts=concepts)


_RECALL_SUMMARY_SYSTEM_PROMPT = """You are an AI coding assistant's memory system.

Summarize the recalled memory snippets below into a concise 2-4 sentence context paragraph.
Focus on facts, decisions, and patterns that are directly relevant to the current query.
Be specific — mention file names, tool names, and concrete decisions when present.
Do not repeat the query back. Do not use bullet points. Plain prose only."""


class RecallMemoryItem(BaseModel):
    title: str = ""
    snippet: str = ""
    type: str = ""
    relevance: float = 0.0


class SummarizeMemoriesRequest(BaseModel):
    """Body for POST /summarize-memories."""

    query: str
    memories: list[RecallMemoryItem]


class SummarizeMemoriesResponse(BaseModel):
    summary: str


@app.post("/summarize-memories", response_model=SummarizeMemoriesResponse)
async def summarize_memories(req: SummarizeMemoriesRequest) -> SummarizeMemoriesResponse:
    """Condense recalled search results into a single context paragraph via LLM.

    Falls back to a bullet-joined title list when the LLM is unavailable.
    """
    import asyncio

    if not req.memories:
        requests_total.labels(route="/summarize-memories", status="200").inc()
        return SummarizeMemoriesResponse(summary="")

    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    client = build_llm_client(settings.resolve_stage_model("summarize"), settings)

    # Build user message listing the memories.
    mem_lines = []
    for i, m in enumerate(req.memories, 1):
        line = f"{i}. [{m.type or 'memory'}] {m.title}"
        if m.snippet:
            line += f" — {m.snippet}"
        mem_lines.append(line)

    user_msg = f"Query: {req.query}\n\nRecalled memories:\n" + "\n".join(mem_lines)

    if client is not None:
        try:
            summary = await asyncio.wait_for(
                client.complete(system=_RECALL_SUMMARY_SYSTEM_PROMPT, user=user_msg),
                timeout=15.0,
            )
            summary = summary.strip()
            if summary:
                requests_total.labels(route="/summarize-memories", status="200").inc()
                return SummarizeMemoriesResponse(summary=summary)
        except Exception:
            logger.debug("summarize-memories LLM failed, using fallback", exc_info=True)

    # Synthetic fallback: join titles as bullets.
    bullets = "\n".join(
        f"• {m.title}" + (f": {m.snippet}" if m.snippet else "")
        for m in req.memories
        if m.title
    )
    requests_total.labels(route="/summarize-memories", status="200").inc()
    return SummarizeMemoriesResponse(summary=bullets)


_TSQUERY_SYSTEM_PROMPT = """You are a query compiler for PostgreSQL ts_query fulltext search.

Your task: Generate 2-3 focused ts_query strings that are SELF-CONTAINED and MEANINGFUL.

CRITICAL RULES:
1. EACH QUERY MUST BE MEANINGFUL ON ITS OWN — never create generic queries without context
2. MULTI-WORD PHRASE SYNTAX: Use <-> for exact phrases: "(word1 <-> word2)"
3. KEEP QUERIES SIMPLE: 2-3 terms per query maximum
4. Use & to combine terms, | for synonyms, parentheses to group
5. LOWERCASE EVERYTHING

EXAMPLES:
Input: "JWT authentication token validation"
Output: {"queries": ["jwt & authentication", "token & validation"]}

Input: "rate limiting middleware configuration"
Output: {"queries": ["(rate <-> limiting) | ratelimit", "middleware & configuration"]}

Input: "database connection pool exhaustion debugging"
Output: {"queries": ["(connection <-> pool) | pool", "pool & (exhaustion | timeout | leak)"]}

Input: "React state management context API"
Output: {"queries": ["react & (state <-> management)", "context & api"]}

Return a JSON object with a "queries" array. No markdown, no explanation."""


class TSQueryRequest(BaseModel):
    """Body for POST /tsquery."""

    query: str


class TSQueryOutput(BaseModel):
    """Structured output from the ts_query compiler agent."""

    queries: list[str]


@app.post("/tsquery", response_model=TSQueryOutput)
async def tsquery(req: TSQueryRequest) -> TSQueryOutput:
    """Convert a natural-language query to PostgreSQL ts_query strings via LLM.

    Uses the configured LLM (via Bifrost) to generate 2-3 structured ts_query
    strings. Falls back to a simple keyword query when the LLM is unavailable.
    """
    import asyncio
    import json as _json
    import re as _re

    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    client = build_llm_client(settings.resolve_stage_model("compress"), settings)

    if client is not None:
        try:
            response = await asyncio.wait_for(
                client.complete(
                    system=_TSQUERY_SYSTEM_PROMPT,
                    user=f'Convert to ts_query strings: "{req.query}"\n\nReturn JSON only.',
                ),
                timeout=15.0,
            )
            text = response.strip()
            # Strip markdown code fences if the model wraps the JSON.
            if "```" in text:
                text = _re.sub(r"```(?:json)?\n?", "", text).strip().rstrip("`")
            data = _json.loads(text)
            queries = data.get("queries", [])
            if queries and isinstance(queries, list):
                valid = [str(q).strip() for q in queries if str(q).strip()][:3]
                if valid:
                    requests_total.labels(route="/tsquery", status="200").inc()
                    return TSQueryOutput(queries=valid)
        except Exception:
            logger.debug("tsquery LLM failed, using fallback", exc_info=True)

    # Synthetic fallback: lowercase and AND-join meaningful terms.
    terms = [t.lower() for t in req.query.split() if len(t) > 2]
    fallback = " & ".join(terms[:4]) if terms else req.query.lower()
    requests_total.labels(route="/tsquery", status="200").inc()
    return TSQueryOutput(queries=[fallback])


class RerankRequest(BaseModel):
    """Body for POST /rerank."""

    query: str
    documents: list[str]
    doc_ids: list[str]
    top_k: int = Field(default=0, description="0 means return all ranked")
    model: str = Field(default="", description="Overrides RERANK_MODEL env var")


class RerankResult(BaseModel):
    id: str
    score: float


class RerankResponse(BaseModel):
    results: list[RerankResult]


@app.post("/rerank", response_model=RerankResponse)
async def rerank(req: RerankRequest) -> RerankResponse:
    """Rerank candidates using Cohere rerank-4-fast via Bifrost.

    Falls back to identity order (RRF order preserved) when Bifrost URL is
    unset or the model field is empty. On Bifrost error, raises HTTP 502.
    """
    import httpx

    settings: Settings = app.state.settings if hasattr(app.state, "settings") else get_settings()
    model = req.model or settings.rerank_model

    if not model or not settings.bifrost_url:
        # Identity fallback: preserve input order with descending dummy scores.
        results = [
            RerankResult(id=doc_id, score=1.0 - i * 0.01)
            for i, doc_id in enumerate(req.doc_ids)
        ]
        requests_total.labels(route="/rerank", status="200").inc()
        return RerankResponse(results=results)

    top_n = req.top_k if req.top_k > 0 else len(req.documents)
    payload = {
        "model": model,
        "query": req.query,
        "documents": req.documents,
        "top_n": top_n,
    }
    headers: dict[str, str] = {"Content-Type": "application/json"}
    if settings.bifrost_api_key:
        headers["Authorization"] = f"Bearer {settings.bifrost_api_key}"

    url = f"{settings.bifrost_url.rstrip('/')}/v1/rerank"
    try:
        async with httpx.AsyncClient(timeout=20.0) as client:
            resp = await client.post(url, json=payload, headers=headers)
            resp.raise_for_status()
            data = resp.json()
    except httpx.HTTPStatusError as exc:
        logger.error("rerank.bifrost_error", status=exc.response.status_code, err=str(exc))
        requests_total.labels(route="/rerank", status="502").inc()
        from fastapi import HTTPException
        raise HTTPException(status_code=502, detail=f"Bifrost rerank error: {exc}") from exc
    except Exception as exc:
        logger.error("rerank.request_failed", err=str(exc))
        requests_total.labels(route="/rerank", status="502").inc()
        from fastapi import HTTPException
        raise HTTPException(status_code=502, detail=f"Rerank request failed: {exc}") from exc

    # Cohere/OpenRouter response: results[].{index, relevance_score}
    # `index` is a 0-based position into the input `documents` list.
    results = []
    for item in data.get("results", []):
        idx = item.get("index")
        score = item.get("relevance_score", 0.0)
        if idx is not None and 0 <= idx < len(req.doc_ids):
            results.append(RerankResult(id=req.doc_ids[idx], score=score))

    results.sort(key=lambda x: x.score, reverse=True)
    requests_total.labels(route="/rerank", status="200").inc()
    return RerankResponse(results=results)


class CompressRequest(RawObservation):
    """A RawObservation plus the project's known abbreviation glossary.

    ``glossary`` (abbreviation→expansion) is request-only: the Go worker ships
    the project's learned abbreviations so we can inline-expand the text the LLM
    sees. It is not part of the persisted observation.
    """

    glossary: dict[str, str] = Field(default_factory=dict)


@app.post("/compress", response_model=CompressedObservation)
async def compress(req: CompressRequest) -> CompressedObservation:
    """Compress a single RawObservation via LLM (or synthetic fallback)."""
    compressor: LLMCompressor = getattr(app.state, "compressor", None) or LLMCompressor(
        client=None,
        settings=app.state.settings if hasattr(app.state, "settings") else get_settings(),
    )
    result = await compressor.compress(req, glossary=req.glossary)
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
            logger.debug("title generation failed", exc_info=True)
    # Fallback: first 8 words
    words = req.content.split()[:8]
    title = " ".join(words)
    if len(title) > 60:
        title = title[:57] + "..."
    requests_total.labels(route="/title", status="200").inc()
    return TitleResponse(title=title, generated=False)

