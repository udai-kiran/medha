# ADR-0006: Extraction defaults to heuristics; spaCy/GLiNER are opt-in

- **Status**: Accepted
- **Date**: 2026-05-26
- **Related**: PRD FR-13, NFR-9, NFR-15; agent_mem.md §"Python Service"

## Context

agent_mem.md proposes a multi-stage extraction pipeline:
spaCy → GLiNER → LLM fallback. Loading those models requires ~1 GB of
downloads and noticeable startup time. NFR-15 caps the Python service at
500 MB RSS under nominal load.

We also need the service to run **without any external deps** for tests,
CI, and the lightweight deployment profile (NFR-9, NFR-24).

## Decision

- The default extraction pipeline is **heuristic-only**: regex + cap rules
  for file paths, function/class identifiers, URLs, emails, title-cased
  phrases. Confidence is ≤0.7 to leave room for a real extractor to win.
- spaCy / GLiNER / LLM stages are **opt-in extras** in `pyproject.toml`
  (`pip install '.[nlp]'`). Their `Extractor` implementations live in the
  same package and slot into the same `ExtractionPipeline` ordered list.
- Production deployments that need higher recall flip
  `EXTRACTION_PIPELINE=full` (or pin the optional deps).

## Consequences

- The skeleton runs everywhere — no model downloads required.
- The 500 MB RSS budget is comfortably under 200 MB without heavy NLP.
- We lose some recall by default; the LLM compressor (Task 13) and the
  heuristic relationships (Task 20) compensate enough for the v1 KPI.
- Users with the appetite for spaCy/GLiNER opt in via one env flag.
