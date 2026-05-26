"""Synthetic (zero-LLM) compression — the reliability floor (FR-11, NFR-9).

This path must NEVER require an API key or network call. It produces a low
but predictable confidence (0.3) so downstream search can prefer LLM-compressed
results when both exist.
"""

from __future__ import annotations

import json
import re
from typing import Any

from medha.models import CompressedObservation, RawObservation
from medha.utils.validators import clip

# Map common tool names → observation type. Unknown tools fall through to
# "tool_use" (a safe generic category that still indexes meaningfully).
_TOOL_TYPE_MAP: dict[str, str] = {
    "read": "file_read",
    "open": "file_read",
    "cat": "file_read",
    "edit": "file_edit",
    "write": "file_edit",
    "patch": "file_edit",
    "shell": "command",
    "bash": "command",
    "exec": "command",
    "run": "command",
    "search": "search",
    "grep": "search",
    "ripgrep": "search",
    "rg": "search",
    "glob": "search",
    "find": "search",
    "fetch": "network",
    "curl": "network",
    "http": "network",
    "test": "test_run",
    "pytest": "test_run",
    "go_test": "test_run",
}

# Match likely file paths in the tool I/O. Captures: ./foo/bar.go, /abs/path.py,
# relative or rooted, with letters/digits/dots/dashes/underscores/slashes.
# The trailing extension list is broad — extend as new languages are encountered.
_FILE_PATH_RE = re.compile(
    r"(?:(?<=[\s\"'`])|^)"
    r"(?:\.{0,2}/)?"
    r"(?:[\w.\-]+/)*"
    r"[\w.\-]+\.(?:"
    r"go|py|ts|tsx|js|jsx|rs|java|kt|swift|rb|php|"
    r"c|h|cc|cpp|hpp|cs|scala|"
    r"md|json|yaml|yml|toml|xml|html|css|sql|sh|bash|"
    r"proto|graphql|tf|dockerfile"
    r")\b"
)


def infer_type(tool_name: str | None) -> str:
    """Return the canonical observation type for a tool name.

    Returns ``"tool_use"`` for unknown tools so the output still has a type.
    """
    if not tool_name:
        return "user_event"
    return _TOOL_TYPE_MAP.get(tool_name.lower(), "tool_use")


def extract_files(*texts: str | None) -> list[str]:
    """Return a deduplicated, order-preserving list of file paths found in inputs."""
    seen: dict[str, None] = {}
    for t in texts:
        if not t:
            continue
        for m in _FILE_PATH_RE.findall(t):
            seen.setdefault(m, None)
    return list(seen.keys())


def _stringify_input(tool_input: Any) -> str:
    """Stable, compact one-line representation of a tool input mapping."""
    if tool_input is None:
        return ""
    if isinstance(tool_input, str):
        return tool_input
    try:
        return json.dumps(tool_input, sort_keys=True, separators=(",", ":"))
    except (TypeError, ValueError):
        return str(tool_input)


def synthetic_compress(raw: RawObservation) -> CompressedObservation:
    """Compress a RawObservation without any LLM call.

    Shape of the output mirrors the LLM path's contract so downstream indexing
    code (BM25 in Task 14, vector in Task 15) doesn't branch on the source.
    """
    tool_name = raw.tool_name or raw.hook_type
    input_str = _stringify_input(raw.tool_input)
    output_str = raw.tool_output or ""
    narrative = " | ".join(part for part in (tool_name, input_str, clip(output_str, 400)) if part)

    files = extract_files(input_str, output_str, raw.user_prompt)
    typ = infer_type(raw.tool_name)
    title = raw.tool_name or raw.hook_type
    subtitle = files[0] if files else ""

    return CompressedObservation(
        id=raw.id,
        sessionId=raw.session_id,
        type=typ,
        title=clip(title, 120),
        subtitle=clip(subtitle, 200),
        facts=[],
        narrative=clip(narrative, 1000),
        concepts=[],
        files=files,
        importance=5,
        confidence=0.3,
    )


class SyntheticCompressor:
    """Stateless callable wrapper around ``synthetic_compress``.

    Kept as a class because Task 13's LLM compressor takes a ``fallback``
    parameter, and "an instance" with a uniform interface is easier to wire.
    """

    def __call__(self, raw: RawObservation) -> CompressedObservation:
        return synthetic_compress(raw)

    @staticmethod
    def name() -> str:
        return "synthetic"
