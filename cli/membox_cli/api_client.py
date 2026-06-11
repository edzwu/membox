from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from typing import Any


class APIClient:
    def __init__(self, base_url: str | None = None) -> None:
        self.base = (
            base_url or os.getenv("MEMBOX_HUB_URL", "http://127.0.0.1:8080")
        ).rstrip("/")

    def ingest_note(self, payload: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/ingest", payload)

    def search(self, payload: dict[str, Any]) -> dict[str, Any]:
        return self._post("/search", payload)

    def ingest_pdf(self, payload: dict[str, Any]) -> dict[str, Any]:
        return self._post("/ingest", payload)

    def _post(self, path: str, payload: dict[str, Any]) -> dict[str, Any]:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        req = urllib.request.Request(
            f"{self.base}{path}",
            data=data,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=300) as resp:
                body = resp.read().decode("utf-8", errors="replace")
                return json.loads(body) if body else {}
        except urllib.error.HTTPError as e:
            detail = e.read().decode("utf-8", errors="replace")
            raise SystemExit(f"HTTP {e.code}: {detail}") from e
