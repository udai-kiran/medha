"""Smoke tests for the M0 Python skeleton."""

from __future__ import annotations

import json

from fastapi.testclient import TestClient

from medha.config import get_settings
from medha.models import CompressedObservation, RawObservation
from medha.utils.validators import clip, is_observation_id, is_session_id


def test_health_ok(client: TestClient) -> None:
    r = client.get("/health")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["model_version"] == "skeleton"
    assert "embedding_provider" in body


def test_metrics_exposition(client: TestClient) -> None:
    # /health was hit at least once by the previous test or this call;
    # /metrics must always be valid Prometheus exposition.
    client.get("/health")
    r = client.get("/metrics")
    assert r.status_code == 200
    assert "agent_mem_py_requests_total" in r.text


def test_settings_no_llm_required() -> None:
    """Missing optional keys must not raise — NFR-9 degraded mode."""
    s = get_settings()
    assert s.port == 5000
    assert s.embedding_provider == "local"
    # has_any_llm just reports state, doesn't fail.
    assert isinstance(s.has_any_llm(), bool)


def test_validators() -> None:
    assert is_observation_id("obs-abc123")
    assert not is_observation_id("xyz")
    assert is_session_id("sess-deadbeef")
    assert not is_session_id("session-1")
    assert clip("hello", 10) == "hello"
    assert clip("hello world", 5) == "hell…"


def test_raw_observation_roundtrip() -> None:
    payload = {
        "id": "obs-1",
        "sessionId": "sess-abc",
        "timestamp": "2026-05-26T12:34:56Z",
        "hookType": "post_tool_use",
        "toolName": "read",
        "toolInput": {"file_path": "/src/auth.ts"},
        "toolOutput": "export function validateToken",
        "raw": {"foo": "bar"},
        "modality": "text",
    }
    obs = RawObservation.model_validate(payload)
    assert obs.session_id == "sess-abc"
    assert obs.tool_input == {"file_path": "/src/auth.ts"}
    # alias-preserving serialization round-trips back to the wire shape.
    again = obs.model_dump(by_alias=True, mode="json")
    assert again["sessionId"] == "sess-abc"
    # also: JSON-encodable
    json.dumps(again)


def test_compressed_observation_defaults() -> None:
    obs = CompressedObservation(id="obs-1", sessionId="sess-abc", type="file_read", title="Read x")
    assert obs.confidence == 0.3
    assert obs.importance == 5
    assert obs.facts == []
