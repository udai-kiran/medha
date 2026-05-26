"""Tests for the heuristic relationship extractor (Task 20)."""

from __future__ import annotations

from fastapi.testclient import TestClient

from medha.extraction import (
    HeuristicRelationshipExtractor,
    co_occurrence_relationships,
    extract_relationships,
)


def test_depends_on_pattern() -> None:
    rels = HeuristicRelationshipExtractor()("validateToken depends on jose for JWT signing.")
    assert any(r.type == "DEPENDS_ON" for r in rels)
    rel = next(r for r in rels if r.type == "DEPENDS_ON")
    assert "validateToken" in rel.source
    assert "jose" in rel.target


def test_implements_pattern() -> None:
    rels = HeuristicRelationshipExtractor()("AuthMiddleware implements TokenValidator interface.")
    assert any(r.type == "IMPLEMENTS" for r in rels)


def test_supersedes_pattern() -> None:
    rels = HeuristicRelationshipExtractor()("ValidateV2 supersedes ValidateV1 across all callers.")
    assert any(r.type == "SUPERSEDES" for r in rels)


def test_relationship_dedup() -> None:
    # Same (src, tgt, type) within one text → one row.
    text = "the foo depends on bar. the foo depends on bar."
    rels = HeuristicRelationshipExtractor()(text)
    assert len(rels) == 1


def test_co_occurrence_fallback_when_no_pattern() -> None:
    rels = extract_relationships("touched src/auth.ts and tests/conftest.py")
    # No verb pattern, so co-occurrence kicks in.
    assert all(r.type == "RELATED_TO" for r in rels)
    assert len(rels) >= 1


def test_co_occurrence_capped() -> None:
    rels = co_occurrence_relationships([f"e{i}" for i in range(20)])
    # 8 entities → C(8,2) = 28 pairs.
    assert len(rels) == 28


def test_relationship_skips_self_loop() -> None:
    rels = HeuristicRelationshipExtractor()("x depends on x")
    assert len(rels) == 0


def test_extract_endpoint_includes_relationships(client: TestClient) -> None:
    body = {
        "text": "validateToken depends on jose for JWT validation in src/auth.ts.",
        "source_observation_id": "obs-1",
    }
    r = client.post("/extract", json=body)
    assert r.status_code == 200, r.text
    out = r.json()
    assert "relationships" in out
    types = {rel["type"] for rel in out["relationships"]}
    assert "DEPENDS_ON" in types or "RELATED_TO" in types
    # Source observation id propagates.
    if out["relationships"]:
        rel = out["relationships"][0]
        # field is sourceObservationId via alias
        assert rel.get("sourceObservationId") == "obs-1" or rel.get("source_observation_id") == "obs-1"
