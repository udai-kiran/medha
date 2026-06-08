"""OpenAI-compatible chat client (used for Bifrost)."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import httpx
import structlog

logger = structlog.get_logger(__name__)


@dataclass
class OpenAICompatibleClient:
    """Async chat-completion client for any OpenAI-compatible endpoint."""

    base_url: str
    api_key: str
    model: str
    timeout: float = 60.0

    @property
    def name(self) -> str:
        host = self.base_url.split("//", 1)[-1].split("/")[0]
        return f"{host}/{self.model}"

    async def complete(
        self, system: str, user: str, *, max_tokens: int = 1024, json_mode: bool = False
    ) -> str:
        headers: dict[str, str] = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"

        payload: dict[str, Any] = {
            "model": self.model,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
            "max_tokens": max_tokens,
        }
        if json_mode:
            payload["response_format"] = {"type": "json_object"}

        async with httpx.AsyncClient(timeout=self.timeout) as client:
            resp = await client.post(
                f"{self.base_url}/chat/completions",
                headers=headers,
                json=payload,
            )
            resp.raise_for_status()
            data: dict[str, Any] = resp.json()

        return str(data["choices"][0]["message"]["content"])
