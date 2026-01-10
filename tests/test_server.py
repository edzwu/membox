# tests/test_server.py
from __future__ import annotations

import os
import threading
from pathlib import Path
from tempfile import TemporaryDirectory

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


def test_mm_get_file_url_cache():
    with TemporaryDirectory() as td:
        td_path = Path(td)
        src = td_path / "source.txt"
        src.write_text("hello membox\n", "utf-8")
        file_url = src.resolve().as_uri()

        cache_dir = td_path / "cache"
        env = os.environ.copy()
        env["MEMBOX_CACHE_DIR"] = str(cache_dir)

        proc = start_server(env=env)
        assert proc.stdin and proc.stdout

        t = threading.Thread(target=_drain, args=(proc,), daemon=True)
        t.start()

        write_message(proc.stdin, dumps(make_request("mm.get", {"url": file_url}, req_id=1)))
        msg1 = loads(read_message(proc.stdout))

        write_message(proc.stdin, dumps(make_request("mm.get", {"url": file_url}, req_id=2)))
        msg2 = loads(read_message(proc.stdout))

        proc.terminate()
        try:
            proc.wait(timeout=2)
        except Exception:
            proc.kill()

        assert msg1["id"] == 1
        assert msg1["result"]["status"] == "downloaded"
        p1 = Path(msg1["result"]["path"])
        assert p1.exists()
        assert p1.read_text("utf-8") == "hello membox\n"

        assert msg2["id"] == 2
        assert msg2["result"]["status"] == "hit"
        p2 = Path(msg2["result"]["path"])
        assert p2 == p1
