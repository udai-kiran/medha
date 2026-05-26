"""SQLite-backed cache for enrichment results.

Keeps us under Wikipedia/Diffbot rate limits across process restarts. The
schema is intentionally minimal — key + JSON value + TTL.
"""

from __future__ import annotations

import json
import sqlite3
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass
class EnrichmentCache:
    """SQLite cache. Default TTL 7 days."""

    path: str
    ttl_seconds: int = 7 * 24 * 3600

    def __post_init__(self) -> None:
        Path(self.path).parent.mkdir(parents=True, exist_ok=True)
        self._conn = sqlite3.connect(self.path, check_same_thread=False)
        self._conn.execute(
            """
            CREATE TABLE IF NOT EXISTS enrichment_cache (
                key        TEXT PRIMARY KEY,
                value_json TEXT NOT NULL,
                fetched_at INTEGER NOT NULL
            )
            """
        )

    def get(self, key: str) -> dict[str, Any] | None:
        cur = self._conn.execute(
            "SELECT value_json, fetched_at FROM enrichment_cache WHERE key = ?",
            (key,),
        )
        row = cur.fetchone()
        if row is None:
            return None
        value_json, fetched_at = row
        if time.time() - fetched_at > self.ttl_seconds:
            return None
        return json.loads(value_json)

    def put(self, key: str, value: dict[str, Any]) -> None:
        self._conn.execute(
            """
            INSERT INTO enrichment_cache (key, value_json, fetched_at)
            VALUES (?, ?, ?)
            ON CONFLICT(key) DO UPDATE SET
                value_json = excluded.value_json,
                fetched_at = excluded.fetched_at
            """,
            (key, json.dumps(value), int(time.time())),
        )
        self._conn.commit()

    def close(self) -> None:
        self._conn.close()
