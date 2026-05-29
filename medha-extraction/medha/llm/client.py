"""LLM client protocol and Bifrost gateway factory.

Bifrost is the only supported gateway. It speaks the OpenAI-compatible
chat-completions API and expects models prefixed with "openrouter/":
e.g. "openrouter/anthropic/claude-3-5-haiku". Pass bare or vendor/model
names and the factory normalises them automatically.

Returns None (→ synthetic fallback) when no model is configured.
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Protocol

if TYPE_CHECKING:
    from medha.config import Settings  # pragma: no cover

logger = logging.getLogger(__name__)


class LLMClient(Protocol):
    """Narrow interface any chat-completion provider must satisfy."""

    async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str: ...

    @property
    def name(self) -> str: ...


def _to_bifrost_model(model: str) -> str:
    """Normalise a model name to the format Bifrost expects: openrouter/<vendor>/<model>."""
    if model.startswith("openrouter/"):
        return model
    if "/" in model:
        return f"openrouter/{model}"
    if model.startswith("claude"):
        vendor = "anthropic"
    elif model.startswith(("gpt-", "o1-", "o3-", "text-embedding")):
        vendor = "openai"
    else:
        vendor = "openai"
    return f"openrouter/{vendor}/{model}"


def build_llm_client(model: str | None, settings: Settings) -> LLMClient | None:
    """Build a Bifrost LLMClient for *model*.

    Returns None (→ synthetic fallback) when no model is configured.
    """
    if not model:
        return None

    normalized = _to_bifrost_model(model)
    logger.info("llm.client_built", extra={"gateway": "bifrost", "model": normalized})

    from medha.llm.openai_compat import OpenAICompatibleClient

    return OpenAICompatibleClient(
        base_url=f"{settings.bifrost_url}/v1",
        api_key=settings.bifrost_api_key,
        model=normalized,
    )
