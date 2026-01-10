from __future__ import annotations

import os
import sys
import threading
from dataclasses import dataclass
from typing import Any, Optional

from client.transport import start_server
from shared.framing import read_message, write_message
from shared.protocol import dumps, loads, make_request


def _default_stderr_sink(line: str) -> None:
    sys.stderr.write("[server] " + line)


def _drain_stderr(proc, sink) -> None:
    if proc.stderr is None:
        return
    for line in iter(proc.stderr.readline, b""):
        try:
            sink(line.decode("utf-8", "replace"))
        except Exception:
            pass


@dataclass
class RpcError(RuntimeError):
    code: int
    message: str
    data: Any = None

    def __str__(self) -> str:
        return f"RPC error {self.code}: {self.message}"


class RpcClient:
    def __init__(
        self,
        *,
        server_module: str = "server.main",
        cwd: Optional[str] = None,
        env: Optional[dict[str, str]] = None,
        stderr_sink=None,
    ) -> None:
        merged_env = os.environ.copy()
        if env:
            merged_env.update(env)
        self._proc = start_server(module=server_module, cwd=cwd, env=merged_env)
        assert self._proc.stdin and self._proc.stdout
        self._stdin = self._proc.stdin
        self._stdout = self._proc.stdout
        self._id = 0

        sink = stderr_sink or _default_stderr_sink
        t = threading.Thread(target=_drain_stderr, args=(self._proc, sink), daemon=True)
        t.start()

    def request(self, method: str, params: Any = None) -> Any:
        self._id += 1
        req = make_request(method, params=params, req_id=self._id)
        write_message(self._stdin, dumps(req))
        msg = loads(read_message(self._stdout))
        if not isinstance(msg, dict) or msg.get("id") != self._id:
            raise RuntimeError(f"unexpected response: {msg!r}")
        if "error" in msg:
            err = msg.get("error") if isinstance(msg.get("error"), dict) else {}
            raise RpcError(
                code=int(err.get("code", -1)),
                message=str(err.get("message", "Unknown error")),
                data=err.get("data"),
            )
        return msg.get("result")

    def close(self) -> None:
        self._proc.terminate()
        try:
            self._proc.wait(timeout=2)
        except Exception:
            self._proc.kill()

    def __enter__(self) -> "RpcClient":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()
