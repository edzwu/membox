from __future__ import annotations

import queue
import shlex
import threading
import time
from pathlib import Path
from typing import Optional

from client.rpc import RpcClient, RpcError


def run(server_module: str = "server.main") -> int:
    try:
        import curses
    except Exception:
        return _run_repl(server_module=server_module)

    def _main(stdscr) -> int:
        return _run_curses(stdscr, server_module=server_module)

    return curses.wrapper(_main)


def _run_repl(*, server_module: str) -> int:
    print("curses unavailable; falling back to simple REPL.")
    with RpcClient(server_module=server_module) as rpc:
        while True:
            try:
                line = input("mm> ").strip()
            except EOFError:
                return 0
            if not line:
                continue
            if line in ("q", "quit", "exit"):
                return 0
            _handle_command_line(line, rpc, print)


def _run_curses(stdscr, *, server_module: str) -> int:
    import curses

    curses.use_default_colors()
    curses.curs_set(1)

    output: list[str] = []
    cmd_q: "queue.Queue[Optional[str]]" = queue.Queue()
    out_q: "queue.Queue[str]" = queue.Queue()
    stop = threading.Event()

    def out(s: str = "") -> None:
        out_q.put(s)

    def worker() -> None:
        def stderr_sink(line: str) -> None:
            # In curses mode, never write directly to stderr/stdout; route to pane.
            out("[server] " + line.rstrip("\n"))

        try:
            with RpcClient(server_module=server_module, stderr_sink=stderr_sink) as rpc:
                while not stop.is_set():
                    try:
                        line = cmd_q.get(timeout=0.1)
                    except queue.Empty:
                        continue
                    if line is None:
                        return
                    _handle_command_line(line, rpc, out)
        except Exception as e:
            out(f"worker crashed: {e!r}")

    t = threading.Thread(target=worker, daemon=True)
    t.start()

    output.extend(
        [
            "membox TUI",
            "Commands: `mm get <url>` | `get <url>` | `help` | `quit`",
            "",
        ]
    )

    stdscr.nodelay(True)

    buf = ""

    def _render() -> None:
        stdscr.erase()
        h, w = stdscr.getmaxyx()
        if h <= 2 or w <= 4:
            stdscr.refresh()
            return

        out_h = h - 2
        in_y = h - 1

        def wrap_line(s: str) -> list[str]:
            if not s:
                return [""]
            width = max(1, w - 1)
            return [s[i : i + width] for i in range(0, len(s), width)]

        wrapped: list[str] = []
        for line in output:
            wrapped.extend(wrap_line(line))

        start = max(0, len(wrapped) - out_h)
        for i, line in enumerate(wrapped[start : start + out_h]):
            stdscr.addnstr(i, 0, line, w - 1)

        stdscr.hline(h - 2, 0, curses.ACS_HLINE, w - 1)

        prompt = "mm> "
        avail = max(0, (w - 1) - len(prompt))
        show_buf = buf[-avail:] if avail else ""
        stdscr.addnstr(in_y, 0, prompt + show_buf, w - 1)
        stdscr.clrtoeol()
        stdscr.refresh()

    _render()

    try:
        while True:
            drained = 0
            while True:
                try:
                    line = out_q.get_nowait()
                except queue.Empty:
                    break
                output.append(line)
                drained += 1

            if drained:
                _render()

            ch = stdscr.getch()
            if ch == -1:
                time.sleep(0.02)
                continue
            if ch in (curses.KEY_ENTER, 10, 13):
                line = buf.strip()
                buf = ""
                output.append("")
                if not line:
                    _render()
                    continue
                if line in ("q", "quit", "exit"):
                    return 0
                output.append(f"mm> {line}")
                cmd_q.put(line)
                _render()
                continue
            if ch in (curses.KEY_BACKSPACE, 127, 8):
                buf = buf[:-1]
                _render()
                continue
            if ch == curses.KEY_RESIZE:
                _render()
                continue
            if ch in (3, 4):  # Ctrl-C / Ctrl-D
                return 0
            if 32 <= ch <= 126:
                buf += chr(ch)
                _render()
    finally:
        stop.set()
        cmd_q.put(None)
        t.join(timeout=1.0)


def _handle_command_line(line: str, rpc: RpcClient, out) -> None:
    if line.startswith("mm "):
        line = line[3:]
    try:
        parts = shlex.split(line)
    except ValueError as e:
        out(f"parse error: {e}")
        return
    if not parts:
        return

    cmd, *rest = parts
    if cmd == "get" and len(rest) == 1:
        url = rest[0]
        out(f"$ mm get {url}")
        out("… fetching …")
        try:
            result = rpc.request("mm.get", {"url": url})
        except RpcError as e:
            out(str(e))
            return
        if not isinstance(result, dict):
            out(f"unexpected result: {result!r}")
            return
        path = result.get("path")
        out(f"status: {result.get('status')}")
        out(f"path: {path}")
        out(f"size: {result.get('size')}")
        out(f"content_type: {result.get('content_type')}")
        out("")
        _preview_file(path, out)
        return

    if cmd in ("help", "?"):
        out("Commands: `mm get <url>` | `get <url>` | `quit`")
        return

    out(f"unknown command: {cmd!r} (try `help`)")


def _preview_file(path: Optional[str], out) -> None:
    if not isinstance(path, str) or not path:
        out("no file path returned")
        return
    p = Path(path)
    if not p.exists():
        out("file missing on disk")
        return

    with open(p, "rb") as f:
        head = f.read(200_000)
    try:
        text = head.decode("utf-8")
    except Exception:
        out("binary preview (first 256 bytes):")
        out(head[:256].hex())
        return

    lines = text.splitlines()
    if len(lines) > 200:
        lines = lines[:200] + ["…(truncated)…"]
    out("text preview:")
    for ln in lines:
        out(ln)
