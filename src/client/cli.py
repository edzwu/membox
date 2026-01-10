# client/cli.py
from __future__ import annotations

import argparse
import json
import sys
from typing import Any, Optional

from client.rpc import RpcClient, RpcError
from client.tui import run as run_tui


def ping(server_module: str = "server.main") -> int:
    with RpcClient(server_module=server_module) as rpc:
        print(rpc.request("ping"))
        return 0


def get(url: str, server_module: str = "server.main") -> int:
    try:
        with RpcClient(server_module=server_module) as rpc:
            result = rpc.request("mm.get", {"url": url})
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0
    except RpcError as e:
        print(str(e), file=sys.stderr)
        return 2


def main(argv: Optional[list[str]] = None) -> int:
    p = argparse.ArgumentParser(prog="mm")
    sub = p.add_subparsers(dest="cmd", required=True)

    p_ping = sub.add_parser("ping", help="send ping and print pong")
    p_ping.add_argument("--server-module", default="server.main")

    p_get = sub.add_parser("get", help="download url with cache (server extension)")
    p_get.add_argument("url")
    p_get.add_argument("--server-module", default="server.main")

    p_tui = sub.add_parser("tui", help="interactive terminal UI")
    p_tui.add_argument("--server-module", default="server.main")

    args = p.parse_args(argv)
    if args.cmd == "ping":
        return ping(server_module=args.server_module)
    if args.cmd == "get":
        return get(args.url, server_module=args.server_module)
    if args.cmd == "tui":
        return run_tui(server_module=args.server_module)

    return 1


if __name__ == "__main__":
    raise SystemExit(main())
