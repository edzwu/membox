# tests/test_protocol.py
from __future__ import annotations

import io

from shared.framing import read_message, write_message


def test_framing_roundtrip():
    buf = io.BytesIO()
    body = b'{"jsonrpc":"2.0","method":"ping","id":1}'
    write_message(buf, body)

    buf.seek(0)
    got = read_message(buf)
    assert got == body
