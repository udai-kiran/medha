"""Tests for the heuristic entity extractor + pipeline (Task 19)."""

from __future__ import annotations

from fastapi.testclient import TestClient

from medha.extraction import (
    ExtractionPipeline,
    HeuristicExtractor,
    classify_subtype,
    default_pipeline,
)


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
    body = {"text": "Edit src/auth.ts and call validateToken()", "source_observation_id": "obs-1"}
    r = client.post("/extract", json=body)
    assert r.status_code == 200, r.text
    out = r.json()
    assert "heuristic" in out["stages_run"]
    names = {e["name"] for e in out["entities"]}
    assert "src/auth.ts" in names
    assert any(n.startswith("validateToken") for n in names)


def test_default_pipeline_returns_pipeline() -> None:
    p = default_pipeline()
    assert any(e.name == "heuristic" for e in p.extractors)
