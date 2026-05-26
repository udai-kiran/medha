"""Tests for the synthetic compression path (Task 11)."""

from __future__ import annotations

from datetime import UTC, datetime

from fastapi.testclient import TestClient

from medha.compression import SyntheticCompressor, synthetic_compress
from medha.compression.synthetic_compressor import extract_files, infer_type
from medha.models import RawObservation


def make_raw(**kwargs: object) -> RawObservation:
    base = dict(
        id="obs-1",
        sessionId="sess-1",
        timestamp=datetime.now(UTC),
        hookType="post_tool_use",
        modality="text",
        raw={},
    )
    base.update(kwargs)
    return RawObservation.model_validate(base)


def test_infer_type_known_tools() -> None:
    assert infer_type("read") == "file_read"
    assert infer_type("Bash") == "command"
    assert infer_type("grep") == "search"
    assert infer_type("pytest") == "test_run"
    assert infer_type("unknown") == "tool_use"
    assert infer_type(None) == "user_event"


def test_extract_files_finds_paths() -> None:
    text = "Read src/auth.ts and tests/conftest.py; also `pkg/config.go` and ./scripts/run.sh"
    files = extract_files(text)
    for want in ("src/auth.ts", "tests/conftest.py", "pkg/config.go", "scripts/run.sh"):
        assert any(f.endswith(want) for f in files), f"missing {want!r} in {files!r}"


def test_extract_files_dedupes_and_preserves_order() -> None:
    a = "src/x.go and pkg/y.py"
    b = "src/x.go again and tests/z.py"
    files = extract_files(a, b)
    assert files.count("src/x.go") == 1
    assert files.index("src/x.go") < files.index("tests/z.py")


def test_synthetic_compress_shape() -> None:
    raw = make_raw(
        toolName="read",
        toolInput={"file_path": "src/auth.ts"},
        toolOutput="export function validateToken() {}",
    )
    c = synthetic_compress(raw)
    assert c.id == "obs-1"
    assert c.session_id == "sess-1"
    assert c.type == "file_read"
    assert c.title == "read"
    assert "src/auth.ts" in c.files
    assert c.subtitle == "src/auth.ts"
    assert c.confidence == 0.3
    assert c.importance == 5
    assert c.facts == []
    assert c.concepts == []
    assert "read" in c.narrative


def test_synthetic_compress_truncates_long_output() -> None:
    long_output = "x" * 10_000
    raw = make_raw(toolName="shell", toolInput={"cmd": "yes"}, toolOutput=long_output)
    c = synthetic_compress(raw)
    assert len(c.narrative) <= 1000


def test_synthetic_compress_handles_empty_input() -> None:
    raw = make_raw(hookType="user_prompt")
    c = synthetic_compress(raw)
    # Empty tool fields should still produce a valid result.
    assert c.id == "obs-1"
    assert c.confidence == 0.3
    assert c.type == "user_event"  # because tool_name is None


def test_compress_endpoint_returns_synthetic_result(client: TestClient) -> None:
    body = {
        "id": "obs-1",
        "sessionId": "sess-1",
        "timestamp": "2026-05-26T12:00:00Z",
        "hookType": "post_tool_use",
        "toolName": "read",
        "toolInput": {"file_path": "/x.go"},
        "toolOutput": "package main",
        "modality": "text",
        "raw": {},
    }
    r = client.post("/compress", json=body)
    assert r.status_code == 200, r.text
    out = r.json()
    assert out["type"] == "file_read"
    assert out["confidence"] == 0.3
    # alias-preserving serialization (Pydantic v2 honours model_config populate_by_name).
    assert out["sessionId"] == "sess-1" or out["session_id"] == "sess-1"


def test_synthetic_compressor_class_callable() -> None:
    c = SyntheticCompressor()
    raw = make_raw(toolName="grep", toolInput={"pattern": "TODO"})
    out = c(raw)
    assert out.type == "search"
    assert c.name() == "synthetic"
