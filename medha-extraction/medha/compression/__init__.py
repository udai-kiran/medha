"""Compression pipeline.

`SyntheticCompressor` is the always-available zero-LLM path (Task 11). Task 13
adds an LLM compressor that falls back to this on timeout/error.
"""

from medha.compression.llm_compressor import (
    LLMClient,
    LLMCompressor,
    LLMCompressorConfig,
    build_prompt,
    parse_response,
)
from medha.compression.synthetic_compressor import SyntheticCompressor, synthetic_compress
from medha.compression.validator import validate_compressed

__all__ = [
    "LLMClient",
    "LLMCompressor",
    "LLMCompressorConfig",
    "SyntheticCompressor",
    "build_prompt",
    "parse_response",
    "synthetic_compress",
    "validate_compressed",
]
