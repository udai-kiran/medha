"""Shared pytest fixtures.

For the skeleton we only need an HTTP client; later tasks add fixtures for
DB, mocked LLMs, etc.
"""

from __future__ import annotations

from collections.abc import Iterator

import pytest
from fastapi.testclient import TestClient

from medha.api import app


@pytest.fixture()
def client() -> Iterator[TestClient]:
    with TestClient(app) as c:
        yield c
