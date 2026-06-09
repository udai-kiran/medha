"""Tests for abbreviation inline-expansion and detection (compression path)."""

from __future__ import annotations

import json
from datetime import UTC, datetime

from medha.compression import build_prompt, expand_abbreviations, parse_response
from medha.models import CompressedObservation, RawObservation

GLOSSARY = {"MFA": "Multi-Factor Authentication", "API": "Application Programming Interface"}


def test_expand_basic() -> None:
    out = expand_abbreviations("We enabled MFA today", GLOSSARY)
    assert out == "We enabled MFA (Multi-Factor Authentication) today"


def test_expand_substring_not_matched() -> None:
    # "API" inside "RAPID" / "APIs" boundaries must not be expanded.
    assert expand_abbreviations("RAPID changes", GLOSSARY) == "RAPID changes"
    assert expand_abbreviations("the APIular thing", GLOSSARY) == "the APIular thing"


def test_expand_skips_already_parenthesised() -> None:
    text = "MFA (Multi-Factor Authentication) is on"
    # Author already expanded it — leave untouched, no double expansion.
    assert expand_abbreviations(text, GLOSSARY) == text


def test_expand_only_first_occurrence() -> None:
    out = expand_abbreviations("MFA then MFA again", GLOSSARY)
    assert out == "MFA (Multi-Factor Authentication) then MFA again"


def test_expand_case_sensitive() -> None:
    # Lowercase "api" must not be expanded (avoids it/IT-style false hits).
    assert expand_abbreviations("the api call", GLOSSARY) == "the api call"


def test_expand_regex_special_abbrev() -> None:
    # An abbrev containing regex metacharacters must be escaped, not interpreted.
    g = {"C++": "C plus plus"}
    assert expand_abbreviations("I use C++ daily", g) == "I use C++ (C plus plus) daily"


def test_expand_empty_inputs() -> None:
    assert expand_abbreviations("", GLOSSARY) == ""
    assert expand_abbreviations("text", {}) == "text"


def test_build_prompt_expands_output() -> None:
    raw = RawObservation.model_validate(
        {
            "id": "obs-1",
            "sessionId": "sess-1",
            "timestamp": datetime.now(UTC),
            "hookType": "post_tool_use",
            "toolName": "read",
            "toolOutput": "Configured MFA for the API gateway",
            "modality": "text",
            "raw": {},
        }
    )
    _, user = build_prompt(raw, GLOSSARY)
    assert "MFA (Multi-Factor Authentication)" in user
    assert "API (Application Programming Interface)" in user


def test_parse_response_extracts_abbreviations() -> None:
    raw = RawObservation.model_validate(
        {
            "id": "obs-1",
            "sessionId": "sess-1",
            "timestamp": datetime.now(UTC),
            "hookType": "post_tool_use",
            "modality": "text",
            "raw": {},
        }
    )
    payload = json.dumps(
        {
            "type": "command",
            "title": "set up sso",
            "importance": 5,
            "abbreviations": {"SSO": "Single Sign-On", "": "ignored", "X": ""},
        }
    )
    out = parse_response(payload, raw)
    assert out is not None
    assert out.abbreviations == {"SSO": "Single Sign-On"}


def test_parse_response_abbreviations_capped() -> None:
    raw = RawObservation.model_validate(
        {
            "id": "obs-1",
            "sessionId": "sess-1",
            "timestamp": datetime.now(UTC),
            "hookType": "post_tool_use",
            "modality": "text",
            "raw": {},
        }
    )
    many = {f"AB{i}": f"expansion number {i}" for i in range(40)}
    payload = json.dumps({"type": "command", "title": "t", "importance": 5, "abbreviations": many})
    out = parse_response(payload, raw)
    assert out is not None
    assert len(out.abbreviations) == 20


def test_compressed_observation_serializes_abbreviations_key() -> None:
    # The Go callback (CompressedCallback.Abbreviations) reads the JSON key
    # "abbreviations"; lock the wire contract on the Python side too.
    c = CompressedObservation(
        id="o", sessionId="s", type="command", title="t",
        abbreviations={"SSO": "Single Sign-On"},
    )
    dumped = c.model_dump(by_alias=True)
    assert dumped["abbreviations"] == {"SSO": "Single Sign-On"}
