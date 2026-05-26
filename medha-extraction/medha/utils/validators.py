"""Lightweight input validation helpers used across the service."""

from __future__ import annotations

import re

_OBSERVATION_ID_RE = re.compile(r"^obs-[A-Za-z0-9_-]{4,}$")
_SESSION_ID_RE = re.compile(r"^sess-[A-Za-z0-9_-]{4,}$")


def is_observation_id(s: str) -> bool:
    """True if ``s`` matches the canonical ``obs-...`` format."""
    return bool(_OBSERVATION_ID_RE.match(s))


def is_session_id(s: str) -> bool:
    """True if ``s`` matches the canonical ``sess-...`` format."""
    return bool(_SESSION_ID_RE.match(s))


def clip(text: str, limit: int) -> str:
    """Return ``text`` truncated to ``limit`` characters, with an ellipsis when cut."""
    if limit <= 0:
        return ""
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 1)] + "…"
