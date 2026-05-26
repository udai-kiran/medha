"""Shape and quality checks on the compression output.

The validator is intentionally lenient — if a field is missing we patch it
to a safe default rather than rejecting the whole observation, because the
synthetic path's outputs are stored even when low quality (it is the
reliability floor).
"""

from __future__ import annotations

from medha.models import CompressedObservation


def validate_compressed(c: CompressedObservation) -> CompressedObservation:
    """Return a sanity-checked copy with patched defaults where necessary.

    Raises ValueError only for unrecoverable problems (missing id/session_id);
    everything else is patched silently and the result is logged by the caller.
    """
    if not c.id:
        raise ValueError("CompressedObservation.id required")
    if not c.session_id:
        raise ValueError("CompressedObservation.session_id required")

    # Clamp ranges that a buggy LLM extractor might violate.
    importance = max(0, min(10, c.importance))
    confidence = max(0.0, min(1.0, c.confidence))

    return c.model_copy(
        update={
            "type": c.type or "tool_use",
            "title": c.title or (c.type or "observation"),
            "importance": importance,
            "confidence": confidence,
        }
    )
