# tests/test_client.py
from __future__ import annotations

from client.cli import ping


def test_client_ping_smoke():
    # just ensure it returns 0
    assert ping() == 0
