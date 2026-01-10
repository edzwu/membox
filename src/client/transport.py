# client/transport.py
from __future__ import annotations

import subprocess
import sys
from pathlib import Path
from typing import Optional


def start_server(
    module: str = "server.main",
    cwd: Optional[str] = None,
) -> subprocess.Popen:
    """
    Start server as a child process using stdio pipes.

    module: python -m <module>
    cwd: repo root (recommended)
    """
    if cwd is None:
        # best effort: assume this file is repo/client/transport.py
        cwd = str(Path(__file__).resolve().parents[1])

    return subprocess.Popen(
        [sys.executable, "-m", module],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,  # keep logs separate
        cwd=cwd,
        bufsize=0,  # unbuffered
    )
