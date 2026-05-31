"""Structured logging via structlog.

Call configure_logging(level) once at startup. All subsequent calls to
structlog.get_logger() or logging.getLogger() emit JSON-formatted records
through the same processor chain so log aggregators see a uniform stream.
"""
from __future__ import annotations

import logging
import sys

import structlog

# Processors shared by both the native structlog path and the stdlib bridge.
_SHARED: list[structlog.typing.Processor] = [
    structlog.contextvars.merge_contextvars,
    structlog.stdlib.add_log_level,
    structlog.stdlib.add_logger_name,
    structlog.processors.TimeStamper(fmt="iso"),
    structlog.processors.StackInfoRenderer(),
]


def configure_logging(level: str = "info") -> None:
    """Configure structlog and the stdlib root logger to emit JSON at *level*.

    Idempotent — calling twice replaces handlers rather than appending.
    Both structlog.get_logger() and logging.getLogger() route through the
    same processor chain and produce identical JSON output.
    """
    lvl = getattr(logging, level.upper(), logging.INFO)

    structlog.configure(
        processors=_SHARED
        + [
            structlog.stdlib.PositionalArgumentsFormatter(),
            structlog.stdlib.ProcessorFormatter.wrap_for_formatter,
        ],
        logger_factory=structlog.stdlib.LoggerFactory(),
        wrapper_class=structlog.make_filtering_bound_logger(lvl),
        cache_logger_on_first_use=True,
    )

    formatter = structlog.stdlib.ProcessorFormatter(
        foreign_pre_chain=_SHARED,
        processors=[
            structlog.stdlib.ProcessorFormatter.remove_processors_meta,
            structlog.processors.JSONRenderer(),
        ],
    )

    handler = logging.StreamHandler(stream=sys.stdout)
    handler.setFormatter(formatter)

    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(lvl)
