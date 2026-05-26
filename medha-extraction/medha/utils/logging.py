"""Structured JSON logging.

Configured once at app startup; later modules just call ``logging.getLogger(__name__)``.
Each record emits a single JSON object — no human-format mixed in — so log
aggregators can parse the stream uniformly.
"""

from __future__ import annotations

import json
import logging
import sys
from typing import Any


class JSONFormatter(logging.Formatter):
    """Render each record as one JSON object with the fields we care about."""

    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "ts": self.formatTime(record, "%Y-%m-%dT%H:%M:%S%z"),
            "level": record.levelname.lower(),
            "logger": record.name,
            "msg": record.getMessage(),
        }
        # Attach structured extras passed via `logger.info("...", extra={...})`.
        for key, value in record.__dict__.items():
            if key in _STDLIB_FIELDS:
                continue
            payload[key] = value
        if record.exc_info:
            payload["exc_info"] = self.formatException(record.exc_info)
        return json.dumps(payload, default=str)


# Fields owned by the stdlib LogRecord; we filter these out before attaching extras
# so each JSON line stays compact and predictable.
_STDLIB_FIELDS = frozenset(
    {
        "name",
        "msg",
        "args",
        "levelname",
        "levelno",
        "pathname",
        "filename",
        "module",
        "exc_info",
        "exc_text",
        "stack_info",
        "lineno",
        "funcName",
        "created",
        "msecs",
        "relativeCreated",
        "thread",
        "threadName",
        "processName",
        "process",
        "message",
        "taskName",
    }
)


def configure_logging(level: str = "info") -> None:
    """Install the JSON formatter on the root logger.

    Idempotent — calling twice replaces handlers rather than appending duplicates.
    """
    lvl = getattr(logging, level.upper(), logging.INFO)
    handler = logging.StreamHandler(stream=sys.stdout)
    handler.setFormatter(JSONFormatter())

    root = logging.getLogger()
    root.handlers = [handler]
    root.setLevel(lvl)
