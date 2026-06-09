"""Tests for the heuristic entity extractor + pipeline (Task 19)."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from medha.config import get_settings
from medha.extraction import (
    ExtractionPipeline,
    HeuristicExtractor,
    LLMExtractor,
    classify_subtype,
    default_pipeline,
)
from medha.extraction.llm_extractor import parse_extract_response


def test_heuristic_extracts_files() -> None:
    h = HeuristicExtractor()
    out = h("Read src/auth.ts and tests/conftest.py", source_observation_id="obs-1")
    names = {e.name: e for e in out}
    assert "src/auth.ts" in names
    assert names["src/auth.ts"].type == "OBJECT"
    assert names["src/auth.ts"].subtype == "FILE"
    assert names["src/auth.ts"].source_observation_ids == ["obs-1"]


def test_heuristic_extracts_function_names() -> None:
    out = HeuristicExtractor()("Call validateToken and parseRequest in middleware.")
    names = {e.name for e in out}
    assert "validateToken" in names
    assert "parseRequest" in names


def test_heuristic_extracts_urls_and_emails() -> None:
    out = HeuristicExtractor()("see https://example.com/docs and contact a@b.co")
    by_subtype = {e.subtype: e.name for e in out}
    assert by_subtype.get("URL") == "https://example.com/docs"
    assert by_subtype.get("EMAIL") == "a@b.co"


def test_heuristic_skips_function_inside_file() -> None:
    # A camelCase substring that happens to live inside a file path shouldn't
    # become a duplicate FUNCTION entity.
    out = HeuristicExtractor()("touched src/userAuth/handler.go")
    file_names = [e.name for e in out if e.subtype == "FILE"]
    fn_names = [e.name for e in out if e.subtype == "FUNCTION"]
    assert any(n.endswith("handler.go") for n in file_names)
    # "userAuth" is part of the file path — should not be re-extracted as
    # a function name.
    assert "userAuth" not in fn_names


def test_pipeline_merges_duplicates_by_confidence() -> None:
    """Same (name, type) emitted twice → keep highest confidence."""

    class _Stub(HeuristicExtractor):
        name = "stub"

    p = ExtractionPipeline(extractors=[HeuristicExtractor(), HeuristicExtractor()])
    # Heuristic is deterministic, so running it twice merges to the same set.
    res = p.extract("Read src/auth.ts", source_observation_id="obs-1")
    files = [e for e in res.entities if e.name == "src/auth.ts"]
    assert len(files) == 1
    assert res.stages_run == ["heuristic", "heuristic"]


def test_classify_subtype() -> None:
    assert classify_subtype("auth.ts", "OBJECT") == "FILE"
    assert classify_subtype("validate()", "OBJECT") == "FUNCTION"
    assert classify_subtype("Alice", "PERSON") is None


def test_extract_endpoint(client: TestClient) -> None:
    # Force the no-network fallback so the endpoint is deterministic regardless
    # of whether an EXTRACT/LLM model happens to be set in the environment.
    client.app.state.extractor = LLMExtractor(client=None, settings=get_settings())
    body = {"text": "Edit src/auth.ts and call validateToken()", "source_observation_id": "obs-1"}
    r = client.post("/extract", json=body)
    assert r.status_code == 200, r.text
    out = r.json()
    assert "heuristic" in out["stages_run"]
    names = {e["name"] for e in out["entities"]}
    assert "src/auth.ts" in names
    assert any(n.startswith("validateToken") for n in names)


class _StubLLM:
    """Canned LLM client for deterministic LLMExtractor tests."""

    def __init__(self, response: str) -> None:
        self._response = response

    @property
    def name(self) -> str:
        return "stub"

    async def complete(
        self, system: str, user: str, *, max_tokens: int = 1024, json_mode: bool = False
    ) -> str:
        return self._response


def test_parse_extract_response_filters_off_list_types() -> None:
    raw = """{
      "entities": [
        {"name": "PostgreSQL", "type": "OBJECT", "subtype": "SERVICE", "confidence": 0.9},
        {"name": "Alice", "type": "PERSON", "confidence": 1.5},
        {"name": "junk", "type": "GADGET"},
        {"name": "PostgreSQL", "type": "OBJECT"},
        {"name": "", "type": "OBJECT"}
      ],
      "relationships": [
        {"source": "Alice", "target": "PostgreSQL", "type": "DEPENDS_ON", "confidence": 0.8},
        {"source": "Alice", "target": "PostgreSQL", "type": "NONSENSE"}
      ]
    }"""
    parsed = parse_extract_response(raw, "obs-1")
    assert parsed is not None
    entities, relationships = parsed
    names = {e.name for e in entities}
    # Off-list type dropped; duplicate (name,type) collapsed; empty name dropped.
    assert names == {"PostgreSQL", "Alice"}
    assert "junk" not in names
    alice = next(e for e in entities if e.name == "Alice")
    assert alice.confidence == 1.0  # clamped from 1.5
    assert all(e.source_observation_ids == ["obs-1"] for e in entities)
    # Only the valid relationship type survives.
    assert len(relationships) == 1
    assert relationships[0].type == "DEPENDS_ON"


def test_parse_extract_response_rejects_non_json() -> None:
    assert parse_extract_response("not json at all", "obs-1") is None


@pytest.mark.asyncio
async def test_llm_extractor_uses_llm_path() -> None:
    raw = '{"entities": [{"name": "Redis", "type": "OBJECT", "subtype": "SERVICE"}], '
    raw += '"relationships": []}'
    ex = LLMExtractor(client=_StubLLM(raw), settings=get_settings())
    out = await ex.extract("we cache sessions in Redis", source_observation_id="obs-9")
    assert out.stages_run == ["llm:stub"]
    assert {e.name for e in out.entities} == {"Redis"}


@pytest.mark.asyncio
async def test_llm_extractor_falls_back_on_bad_json() -> None:
    ex = LLMExtractor(client=_StubLLM("garbage not json"), settings=get_settings())
    out = await ex.extract("Read src/auth.ts", source_observation_id="obs-2")
    # Parse failure → heuristic fallback path.
    assert "heuristic" in out.stages_run
    assert any(e.name == "src/auth.ts" for e in out.entities)


@pytest.mark.asyncio
async def test_llm_extractor_no_client_uses_heuristic() -> None:
    ex = LLMExtractor(client=None, settings=get_settings())
    out = await ex.extract("Read src/auth.ts", source_observation_id="obs-3")
    assert "heuristic" in out.stages_run
    assert ex.name == "heuristic"


def test_default_pipeline_returns_pipeline() -> None:
    p = default_pipeline()
    assert any(e.name == "heuristic" for e in p.extractors)
