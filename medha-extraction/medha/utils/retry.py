"""Exponential-backoff retry decorator for external calls.

Kept dependency-free so the skeleton has no extra pin. Tasks 13/30 (LLM,
enrichment) use this around their network calls.
"""

from __future__ import annotations

import asyncio
import functools
import random
from collections.abc import Awaitable, Callable
from typing import TypeVar

import structlog

_T = TypeVar("_T")
logger = structlog.get_logger(__name__)


def async_retry(
    attempts: int = 3,
    base_delay: float = 0.5,
    max_delay: float = 8.0,
    jitter: float = 0.25,
    exceptions: tuple[type[BaseException], ...] = (Exception,),
) -> Callable[[Callable[..., Awaitable[_T]]], Callable[..., Awaitable[_T]]]:
    """Retry an async function with capped exponential backoff + jitter.

    Args:
        attempts: Total tries including the first. ``attempts=1`` disables retry.
        base_delay: First sleep, in seconds.
        max_delay: Upper bound on any single sleep.
        jitter: Random multiplier (±jitter) applied to each delay.
        exceptions: Which exception types trigger a retry.
    """

    def decorator(fn: Callable[..., Awaitable[_T]]) -> Callable[..., Awaitable[_T]]:
        @functools.wraps(fn)
        async def wrapper(*args: object, **kwargs: object) -> _T:
            delay = base_delay
            last_exc: BaseException | None = None
            for attempt in range(1, attempts + 1):
                try:
                    return await fn(*args, **kwargs)
                except exceptions as exc:  # noqa: PERF203 — explicit catch for retry
                    last_exc = exc
                    if attempt == attempts:
                        break
                    sleep = min(max_delay, delay) * (1 + random.uniform(-jitter, jitter))  # noqa: S311
                    logger.warning(
                        "retry.scheduled",
                        fn=fn.__name__,
                        attempt=attempt,
                        sleep_s=round(sleep, 3),
                    )
                    await asyncio.sleep(sleep)
                    delay *= 2
            assert last_exc is not None  # mypy: exceptions branch always sets this
            raise last_exc

        return wrapper

    return decorator
