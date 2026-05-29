"""Shared pytest fixtures.

For the skeleton we only need an HTTP client; later tasks add fixtures for
DB, mocked LLMs, etc.
"""

from __future__ import annotations

import os
from collections.abc import Iterator

import pytest
from fastapi.testclient import TestClient

from medha.api import app

# BIFROST_URL is required by Settings but unavailable in CI/local test runs.
# Set a dummy value so that tests that don't exercise the LLM path can still
# construct Settings() and start the app.
os.environ.setdefault("BIFROST_URL", "http://localhost:8080")


@pytest.fixture()
def client() -> Iterator[TestClient]:
    with TestClient(app) as c:
        yield c
