"""Runtime settings for the Python service.

All values come from environment variables. BIFROST_URL is required — the
service will not start without it. All other LLM options are model names only;
the gateway is always Bifrost.
"""

from __future__ import annotations

from typing import Literal

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


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

    # Bifrost gateway — required
    bifrost_url: str = Field(alias="BIFROST_URL")
    bifrost_api_key: str = Field(default="", alias="BIFROST_API_KEY")

    # LLM models (per-stage overrides fall back to llm_model)
    llm_model: str | None = Field(default=None, alias="LLM_MODEL")
    compress_model: str | None = Field(default=None, alias="COMPRESS_MODEL")
    summarize_model: str | None = Field(default=None, alias="SUMMARIZE_MODEL")
    extract_model: str | None = Field(default=None, alias="EXTRACT_MODEL")

    # Embedding model — if set, Bifrost is used; if unset, local hashing fallback
    embedding_model: str = Field(default="", alias="EMBEDDING_MODEL")

    # Feature flags
    auto_compress: bool = Field(default=False, alias="AGENTMEMORY_AUTO_COMPRESS")

    # Enrichment
    wikipedia_rate_limit: float = Field(default=0.5, alias="WIKIPEDIA_RATE_LIMIT")
    diffbot_api_key: str | None = Field(default=None, alias="DIFFBOT_API_KEY")

    def resolve_stage_model(self, stage: Literal["compress", "summarize", "extract"]) -> str | None:
        """Return the model name for a stage, falling back to LLM_MODEL default."""
        override = {
            "compress": self.compress_model,
            "summarize": self.summarize_model,
            "extract": self.extract_model,
        }.get(stage)
        return override or self.llm_model

    def embedding_fingerprint(self) -> str:
        """Canonical model identity for the vector index.

        Strips the vendor prefix so that switching gateway does not falsely
        indicate an index mismatch.
        """
        model = self.embedding_model or "local"
        if "/" in model:
            model = model.split("/", 1)[1]
        return model


def get_settings() -> Settings:
    """Build a fresh Settings instance.

    Returned non-cached so tests can monkeypatch env vars between calls. Production
    callers typically grab one instance in the FastAPI lifespan handler and reuse it.
    """
    return Settings()
