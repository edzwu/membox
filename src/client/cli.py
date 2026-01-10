# client/cli.py
from __future__ import annotations

import argparse
import sys
import threading
from typing import Any, Optional

from client.transport import start_server
from shared.framing import read_message, write_message
from shared.protocol import dumps, loads, make_request


def _drain_stderr(proc) -> None:
    # 防止子进程 stderr 堆积卡住（尤其在 Windows）
    if proc.stderr is None:
        return
    for line in iter(proc.stderr.readline, b""):
        try:
            sys.stderr.write("[server] " + line.decode("utf-8", "replace"))
        except Exception:
            pass


def ping(server_module: str = "server.main") -> int:
    proc = start_server(module=server_module)
    assert proc.stdin and proc.stdout

    t = threading.Thread(target=_drain_stderr, args=(proc,), daemon=True)
    t.start()

    req = make_request("ping", params=None, req_id=1)
    write_message(proc.stdin, dumps(req))

    body = read_message(proc.stdout)
    msg = loads(body)

    # clean up
    proc.terminate()
    try:
        proc.wait(timeout=2)
    except Exception:
        proc.kill()

    if isinstance(msg, dict) and "result" in msg:
        print(msg["result"])
        return 0
    print(f"unexpected response: {msg!r}", file=sys.stderr)
    return 2


def main(argv: Optional[list[str]] = None) -> int:
    p = argparse.ArgumentParser(prog="client")
    sub = p.add_subparsers(dest="cmd", required=True)

    p_ping = sub.add_parser("ping", help="send ping and print pong")
    p_ping.add_argument("--server-module", default="server.main")

    args = p.parse_args(argv)
    if args.cmd == "ping":
        return ping(server_module=args.server_module)

    return 1


if __name__ == "__main__":
    raise SystemExit(main())
