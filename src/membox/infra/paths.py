from __future__ import annotations
import os
from pathlib import Path

def membox_home() -> Path:
    base = os.environ.get("MEMBOX_HOME")
    if base:
        return Path(base).expanduser()
    return Path.home() / ".membox"
