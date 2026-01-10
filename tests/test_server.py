# tests/test_server.py
from __future__ import annotations

import threading

from client.transport import start_server
from shared.framing import read_message, write_message
from shared.protocol import dumps, loads, make_request


def _drain(proc):
    if proc.stderr is None:
        return
    for _ in iter(proc.stderr.readline, b""):
        pass


def test_ping_pong():
    proc = start_server()
    assert proc.stdin and proc.stdout

    t = threading.Thread(target=_drain, args=(proc,), daemon=True)
    t.start()

    write_message(proc.stdin, dumps(make_request("ping", req_id=1)))
    msg = loads(read_message(proc.stdout))

    proc.terminate()
    try:
        proc.wait(timeout=2)
    except Exception:
        proc.kill()

    assert msg["id"] == 1
    assert msg["result"] == "pong"
