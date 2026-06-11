from __future__ import annotations

import os
from pathlib import Path

# MVP: backend 路径硬编码或从环境变量读取
# Phase 2: 从 ~/.config/mm/backends.toml 加载

_BACKENDS: dict[str, Path] = {}


def _load() -> None:
    global _BACKENDS
    if _BACKENDS:
        return
    # 默认映射
    defaults = {
        "work": Path.home() / "work" / "notes",
        "personal": Path.home() / "personal" / "diary",
    }
    for name, default_path in defaults.items():
        env_key = f"MEMBOX_BACKEND_{name.upper()}_ROOT"
        root = os.getenv(env_key, str(default_path))
        _BACKENDS[name] = Path(root).expanduser()


def get_backend_root(name: str) -> Path:
    _load()
    if name not in _BACKENDS:
        raise ValueError(f"Unknown backend: {name}. Available: {list(_BACKENDS.keys())}")
    return _BACKENDS[name]


def list_backends() -> dict[str, Path]:
    _load()
    return dict(_BACKENDS)
