"""POLE+O type taxonomy + subtype heuristics (DESIGN.md "POLE+O")."""

from __future__ import annotations

from typing import Final

POLE_O_TYPES: Final = ("PERSON", "OBJECT", "LOCATION", "EVENT", "ORGANIZATION")

# Subtype hints by entity type. Used by classify_subtype to refine a base
# extraction (e.g. "Jose" is OBJECT/LIBRARY rather than OBJECT/generic).
_SUBTYPE_HINTS: dict[str, dict[str, tuple[str, ...]]] = {
    "OBJECT": {
        "FILE": (".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".sql", ".sh"),
        "FUNCTION": ("()",),
    },
}


def classify_subtype(name: str, base_type: str) -> str | None:
    """Best-effort subtype assignment for an extracted entity."""
    hints = _SUBTYPE_HINTS.get(base_type, {})
    lower = name.lower()
    for subtype, needles in hints.items():
        for needle in needles:
            if needle in lower:
                return subtype
    return None
