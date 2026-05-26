"""Runtime settings for the Python service.

All values come from environment variables. Optional LLM keys are *not*
required for startup — missing keys disable a code path rather than block
the service (NFR-9, "degraded but healthy").
"""

from __future__ import annotations

from typing import Literal

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

EmbeddingProvider = Literal["local", "openai", "gemini", "voyage"]


class Settings(BaseSettings):
    """Typed view of every env var the Python service reads."""

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    # Server
    port: int = Field(default=5000, alias="PY_PORT")
    log_level: str = Field(default="info", alias="PY_LOG_LEVEL")

    # LLM providers (all optional)
    anthropic_api_key: str | None = Field(default=None, alias="ANTHROPIC_API_KEY")
    openai_api_key: str | None = Field(default=None, alias="OPENAI_API_KEY")
    gemini_api_key: str | None = Field(default=None, alias="GEMINI_API_KEY")

    # Embedding provider
    embedding_provider: EmbeddingProvider = Field(default="local", alias="EMBEDDING_PROVIDER")
    voyage_api_key: str | None = Field(default=None, alias="VOYAGE_API_KEY")

    # Feature flags
    auto_compress: bool = Field(default=False, alias="AGENTMEMORY_AUTO_COMPRESS")

    # Enrichment
    wikipedia_rate_limit: float = Field(default=0.5, alias="WIKIPEDIA_RATE_LIMIT")
    diffbot_api_key: str | None = Field(default=None, alias="DIFFBOT_API_KEY")

    def has_any_llm(self) -> bool:
        """True if at least one LLM provider key is present."""
        return any((self.anthropic_api_key, self.openai_api_key, self.gemini_api_key))


def get_settings() -> Settings:
    """Build a fresh Settings instance.

    Returned non-cached so tests can monkeypatch env vars between calls. Production
    callers typically grab one instance in the FastAPI lifespan handler and reuse it.
    """
    return Settings()
