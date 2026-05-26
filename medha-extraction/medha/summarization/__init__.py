"""Conversation / session summarization (Task 21).

Same provider pattern as Task 13's LLM compressor: try the configured LLM,
fall back to a synthetic summarizer that just extracts narratives + file
lists. The synthetic path is the floor — it always works, with no key.
"""

from medha.summarization.session import (
    ObservationDigest,
    SessionSummarizer,
    SyntheticSessionSummarizer,
    synthetic_session_summary,
)

__all__ = [
    "ObservationDigest",
    "SessionSummarizer",
    "SyntheticSessionSummarizer",
    "synthetic_session_summary",
]
