"""Tests for the LLM compressor (Task 13) — fallback + parse behaviour."""

from __future__ import annotations

import asyncio
from datetime import UTC, datetime

import pytest

from medha.compression import (
    LLMClient,
    LLMCompressor,
    LLMCompressorConfig,
    parse_response,
)
from medha.config import Settings
from medha.models import RawObservation


def make_raw() -> RawObservation:
    return RawObservation.model_validate(
        {
            "id": "obs-1",
            "sessionId": "sess-1",
            "timestamp": datetime.now(UTC),
            "hookType": "post_tool_use",
            "toolName": "read",
            "toolInput": {"file_path": "src/auth.ts"},
            "toolOutput": "export function validateToken() {}",
            "modality": "text",
            "raw": {},
        }
    )


class _FakeClient(LLMClient):
    """LLMClient stub that returns a canned response."""

    def __init__(self, response: str, *, raise_exc: BaseException | None = None) -> None:
        self.response = response
        self.raise_exc = raise_exc
        self.calls = 0

    async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str:
        self.calls += 1
        if self.raise_exc is not None:
            raise self.raise_exc
        return self.response

    @property
    def name(self) -> str:
        return "fake"


_WELL_FORMED = """
<compressed>
  <type>file_read</type>
  <title>Read auth.ts</title>
  <subtitle>src/auth.ts</subtitle>
  <facts>
    <fact>Validates JWT tokens</fact>
    <fact>Uses jose library</fact>
  </facts>
  <narrative>Examined authentication middleware implementing JWT validation.</narrative>
  <concepts>
    <concept>authentication</concept>
    <concept>jwt</concept>
  </concepts>
  <files>
    <file>src/auth.ts</file>
  </files>
  <importance>7</importance>
</compressed>
"""


def test_parse_response_well_formed() -> None:
    raw = make_raw()
    out = parse_response(_WELL_FORMED, raw)
    assert out is not None
    assert out.type == "file_read"
    assert "Validates JWT tokens" in out.facts
    assert "jwt" in out.concepts
    assert out.importance == 7
    assert out.confidence == 0.85


def test_parse_response_no_envelope_returns_none() -> None:
    assert parse_response("nope, no XML here", make_raw()) is None


def test_parse_response_lenient_recovers() -> None:
    """Malformed XML (e.g. orphan tag) should still extract scalar fields."""
    text = (
        "<compressed>"
        "<type>command</type>"
        "<title>run tests</title>"
        # missing close on importance
        "<importance>9"
        "</compressed>"
    )
    out = parse_response(text, make_raw())
    assert out is not None
    assert out.type == "command"
    assert out.title == "run tests"


@pytest.mark.asyncio()
async def test_compressor_falls_back_when_no_client() -> None:
    settings = Settings()  # no API keys
    c = LLMCompressor(client=None, settings=settings)
    out = await c.compress(make_raw())
    assert out.confidence == 0.3  # synthetic fingerprint


@pytest.mark.asyncio()
async def test_compressor_falls_back_when_client_raises() -> None:
    settings = Settings(_env_file=None, ANTHROPIC_API_KEY="sk-test")  # type: ignore[call-arg]
    client = _FakeClient("", raise_exc=RuntimeError("boom"))
    c = LLMCompressor(client=client, settings=settings)
    out = await c.compress(make_raw())
    assert out.confidence == 0.3  # synthetic
    assert client.calls == 1


@pytest.mark.asyncio()
async def test_compressor_falls_back_on_timeout() -> None:
    settings = Settings(_env_file=None, ANTHROPIC_API_KEY="sk-test")  # type: ignore[call-arg]

    class Slow(_FakeClient):
        async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str:  # noqa: D401
            await asyncio.sleep(5)
            return self.response

    client = Slow(_WELL_FORMED)
    c = LLMCompressor(client=client, settings=settings, config=LLMCompressorConfig(timeout_s=0.05))
    out = await c.compress(make_raw())
    assert out.confidence == 0.3


@pytest.mark.asyncio()
async def test_compressor_uses_llm_when_available() -> None:
    settings = Settings(_env_file=None, ANTHROPIC_API_KEY="sk-test")  # type: ignore[call-arg]
    client = _FakeClient(_WELL_FORMED)
    c = LLMCompressor(client=client, settings=settings)
    out = await c.compress(make_raw())
    assert out.confidence >= 0.7  # parsed from LLM
    assert out.type == "file_read"
    assert client.calls == 1
