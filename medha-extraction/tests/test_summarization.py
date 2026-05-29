"""Tests for the session summarizer (Task 21)."""

from __future__ import annotations

import asyncio

import pytest
from fastapi.testclient import TestClient

from medha.compression.llm_compressor import LLMClient
from medha.config import Settings
from medha.summarization import (
    ObservationDigest,
    SessionSummarizer,
    synthetic_session_summary,
)


def _digests() -> list[ObservationDigest]:
    return [
        ObservationDigest(
            title="Read auth.ts",
            narrative="Examined the JWT validation middleware. Decided to use jose over jsonwebtoken.",
            concepts=["auth", "jwt"],
            files=["src/auth.ts"],
            facts=["Uses jose library"],
        ),
        ObservationDigest(
            title="Add token expiry",
            narrative="Set token expiry to 1 hour. Use HS256 algorithm.",
            concepts=["jwt", "security"],
            files=["src/auth.ts", "src/config.ts"],
            facts=["Expiry: 1h"],
        ),
        ObservationDigest(
            title="Wire middleware",
            narrative="Mounted the middleware on /api/* routes.",
            concepts=["auth"],
            files=["src/server.ts"],
        ),
    ]


def test_synthetic_summary_minimum() -> None:
    s = synthetic_session_summary("sess-1", _digests())
    assert s.session_id == "sess-1"
    assert "auth" in s.title.lower() or "jwt" in s.title.lower()
    assert s.narrative
    assert s.files_modified
    assert "src/auth.ts" in s.files_modified
    assert any("jose" in d.lower() or "use" in d.lower() for d in s.key_decisions)


def test_synthetic_summary_empty() -> None:
    s = synthetic_session_summary("sess-empty", [])
    assert s.title == "Empty session"
    assert s.narrative


def test_summarize_endpoint(client: TestClient) -> None:
    body = {
        "sessionId": "sess-1",
        "observations": [
            {
                "title": "Read auth.ts",
                "narrative": "Use jose library for JWT.",
                "concepts": ["auth"],
                "files": ["src/auth.ts"],
                "facts": [],
            }
        ],
    }
    r = client.post("/summarize", json=body)
    assert r.status_code == 200, r.text
    body = r.json()
    assert body.get("sessionId") == "sess-1" or body.get("session_id") == "sess-1"
    assert body["title"]


class _NoClient(LLMClient):
    @property
    def name(self) -> str:
        return "none"

    async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str:
        raise NotImplementedError


@pytest.mark.asyncio()
async def test_summarizer_falls_back_no_llm() -> None:
    settings = Settings()
    s = SessionSummarizer(client=None, settings=settings)
    out = await s.summarize("sess-1", _digests())
    assert out.session_id == "sess-1"
    assert out.title


@pytest.mark.asyncio()
async def test_summarizer_falls_back_on_error() -> None:
    class Boom(LLMClient):
        @property
        def name(self) -> str:
            return "boom"

        async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str:
            raise RuntimeError("nope")

    settings = Settings(_env_file=None, BIFROST_URL="http://localhost:8080")  # type: ignore[call-arg]
    s = SessionSummarizer(client=Boom(), settings=settings)
    out = await s.summarize("sess-1", _digests())
    # Synthetic path returned ⇒ deterministic title not "<unfilled>".
    assert out.title


@pytest.mark.asyncio()
async def test_summarizer_parses_llm_response() -> None:
    class Good(LLMClient):
        @property
        def name(self) -> str:
            return "good"

        async def complete(self, system: str, user: str, *, max_tokens: int = 1024) -> str:
            return (
                "<summary>"
                "<title>Implement JWT</title>"
                "<narrative>We added JWT validation and chose jose.</narrative>"
                "<decisions><d>Use jose</d><d>1h expiry</d></decisions>"
                "<files><f>src/auth.ts</f></files>"
                "<concepts><c>auth</c><c>jwt</c></concepts>"
                "</summary>"
            )

    settings = Settings(_env_file=None, BIFROST_URL="http://localhost:8080")  # type: ignore[call-arg]
    s = SessionSummarizer(client=Good(), settings=settings)
    out = await s.summarize("sess-1", _digests())
    assert out.title == "Implement JWT"
    assert "Use jose" in out.key_decisions
    assert "src/auth.ts" in out.files_modified
