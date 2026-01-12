from __future__ import annotations

import queue
import os
import shlex
import shutil
import subprocess
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
    last_file_path: Optional[str] = None
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
            normalized = line[3:] if line.startswith("mm ") else line
            if normalized.strip() in ("vim", "open"):
                _open_in_editor_non_curses(last_file_path, print)
                continue
            new_path = _handle_command_line(line, rpc, print)
            if isinstance(new_path, str) and new_path:
                last_file_path = new_path


def _run_curses(stdscr, *, server_module: str) -> int:
    import curses

    curses.use_default_colors()
    curses.curs_set(1)

    output: list[str] = []
    cmd_q: "queue.Queue[Optional[str]]" = queue.Queue()
    out_q: "queue.Queue[str]" = queue.Queue()
    event_q: "queue.Queue[tuple[str, str]]" = queue.Queue()
    stop = threading.Event()
    last_file_path: Optional[str] = None

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
                    new_path = _handle_command_line(line, rpc, out)
                    if isinstance(new_path, str) and new_path:
                        event_q.put(("file", new_path))
        except Exception as e:
            out(f"worker crashed: {e!r}")

    t = threading.Thread(target=worker, daemon=True)
    t.start()

    output.extend(
        [
            "membox TUI",
            "Commands: `mm get <url>` | `get <url>` | `vim` | `help` | `quit`",
            "Keys: Enter=send, v=open last file in vim",
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

            while True:
                try:
                    ev, payload = event_q.get_nowait()
                except queue.Empty:
                    break
                if ev == "file":
                    last_file_path = payload

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

                normalized = line[3:] if line.startswith("mm ") else line
                if normalized.strip() in ("vim", "open"):
                    _open_in_vim(stdscr, last_file_path, output.append)
                else:
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
            if ch in (ord("v"), ord("V")):
                _open_in_vim(stdscr, last_file_path, output.append)
                _render()
                continue
            if 32 <= ch <= 126:
                buf += chr(ch)
                _render()
    finally:
        stop.set()
        cmd_q.put(None)
        t.join(timeout=1.0)


def _handle_command_line(line: str, rpc: RpcClient, out) -> Optional[str]:
    if line.startswith("mm "):
        line = line[3:]
    try:
        parts = shlex.split(line)
    except ValueError as e:
        out(f"parse error: {e}")
        return None
    if not parts:
        return None

    cmd, *rest = parts
    if cmd == "get" and len(rest) == 1:
        url = rest[0]
        out(f"$ mm get {url}")
        out("… fetching …")
        try:
            result = rpc.request("mm.get", {"url": url})
        except RpcError as e:
            out(str(e))
            return None
        if not isinstance(result, dict):
            out(f"unexpected result: {result!r}")
            return None
        path = result.get("path")
        out(f"status: {result.get('status')}")
        out(f"path: {path}")
        out(f"size: {result.get('size')}")
        out(f"content_type: {result.get('content_type')}")
        out("")
        _preview_file(path, out)
        if isinstance(path, str) and path:
            out("tip: press `v` or type `vim` to open the last file")
            return path
        return None

    if cmd in ("vim", "open"):
        out("type `vim` in curses mode or set $EDITOR in non-curses mode")
        return None

    if cmd in ("help", "?"):
        out("Commands: `mm get <url>` | `get <url>` | `vim` | `help` | `quit`")
        out("Keys: Enter=send, v=open last file in vim")
        return None

    out(f"unknown command: {cmd!r} (try `help`)")
    return None


def _open_in_vim(stdscr, path: Optional[str], out) -> None:
    if not isinstance(path, str) or not path:
        out("no last file to open (run `mm get <url>` first)")
        return

    editor_env = os.environ.get("EDITOR")
    if editor_env:
        editor_argv = shlex.split(editor_env)
    else:
        editor_argv = ["nvim"] if shutil.which("nvim") else ["vim"]

    exe = editor_argv[0] if editor_argv else "vim"
    if not shutil.which(exe):
        out(f"editor not found: {exe!r} (set $EDITOR or install vim/nvim)")
        return

    # If inside tmux, open vim in a split pane (separate process) for a true "pane" experience.
    if os.environ.get("TMUX") and shutil.which("tmux"):
        cmd = shlex.join([*editor_argv, path])
        subprocess.call(["tmux", "split-window", "-v", "-p", "70", cmd])
        out(f"opened in tmux split: {path}")
        return

    # Fallback: suspend curses, run editor full-screen, then resume.
    try:
        import curses

        curses.def_prog_mode()
        curses.endwin()
        subprocess.call([*editor_argv, path])
    finally:
        try:
            curses.reset_prog_mode()
            curses.curs_set(1)
            stdscr.nodelay(True)
            stdscr.refresh()
        except Exception:
            pass


def _open_in_editor_non_curses(path: Optional[str], out) -> None:
    if not isinstance(path, str) or not path:
        out("no last file to open (run `mm get <url>` first)")
        return

    editor_env = os.environ.get("EDITOR")
    if editor_env:
        editor_argv = shlex.split(editor_env)
    else:
        editor_argv = ["nvim"] if shutil.which("nvim") else ["vim"]

    exe = editor_argv[0] if editor_argv else "vim"
    if not shutil.which(exe):
        out(f"editor not found: {exe!r} (set $EDITOR or install vim/nvim)")
        return

    subprocess.call([*editor_argv, path])


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
