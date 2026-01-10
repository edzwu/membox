from __future__ import annotations

import json
import os
import time
import urllib.parse
import urllib.request
from dataclasses import asdict, dataclass
from hashlib import sha256
from pathlib import Path
from typing import Any, Optional

from shared.errors import JsonRpcError, INVALID_PARAMS
from server.handlers import HandlerRegistry


@dataclass(frozen=True)
class GetResult:
    status: str  # "hit" | "downloaded"
    url: str
    path: str
    filename: str
    size: int
    content_type: Optional[str] = None
    fetched_at: Optional[float] = None


def register(reg: HandlerRegistry) -> None:
    reg.register("mm.get", _handle_get)


def _handle_get(params: Any) -> dict[str, Any]:
    if not isinstance(params, dict):
        raise JsonRpcError(INVALID_PARAMS, "params must be an object")
    url = params.get("url")
    if not isinstance(url, str) or not url:
        raise JsonRpcError(INVALID_PARAMS, "params.url must be a non-empty string")

    cache_dir = _get_cache_dir()
    key = sha256(url.encode("utf-8")).hexdigest()
    base_name = _best_filename_from_url(url)
    dir_path = cache_dir / key
    dir_path.mkdir(parents=True, exist_ok=True)
    file_path = dir_path / base_name
    meta_path = dir_path / "meta.json"

    if file_path.exists():
        content_type, fetched_at = _read_meta(meta_path)
        size = file_path.stat().st_size
        return asdict(
            GetResult(
                status="hit",
                url=url,
                path=str(file_path),
                filename=base_name,
                size=size,
                content_type=content_type,
                fetched_at=fetched_at,
            )
        )

    content_type, fetched_at, size = _download(url, file_path)
    _write_meta(meta_path, url=url, content_type=content_type, fetched_at=fetched_at, size=size, filename=base_name)
    return asdict(
        GetResult(
            status="downloaded",
            url=url,
            path=str(file_path),
            filename=base_name,
            size=size,
            content_type=content_type,
            fetched_at=fetched_at,
        )
    )


def _get_cache_dir() -> Path:
    override = os.environ.get("MEMBOX_CACHE_DIR")
    if override:
        return Path(override).expanduser().resolve()

    xdg = os.environ.get("XDG_CACHE_HOME")
    root = Path(xdg).expanduser() if xdg else Path.home() / ".cache"
    return (root / "membox").resolve()


def _best_filename_from_url(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    name = Path(urllib.parse.unquote(parsed.path)).name
    name = name.strip().strip(".")
    if not name:
        name = "download"
    name = "".join(ch if ch.isalnum() or ch in ("-", "_", ".", " ") else "_" for ch in name)
    if len(name) > 200:
        name = name[:200]
    return name


def _download(url: str, file_path: Path) -> tuple[Optional[str], float, int]:
    fetched_at = time.time()
    req = urllib.request.Request(url, headers={"User-Agent": "membox/0.1"})
    with urllib.request.urlopen(req) as resp:
        content_type = getattr(resp.headers, "get", lambda _k, _d=None: None)("Content-Type", None)
        size = 0
        with open(file_path, "wb") as f:
            while True:
                chunk = resp.read(64 * 1024)
                if not chunk:
                    break
                f.write(chunk)
                size += len(chunk)
        return content_type, fetched_at, size


def _read_meta(meta_path: Path) -> tuple[Optional[str], Optional[float]]:
    try:
        data = json.loads(meta_path.read_text("utf-8"))
        content_type = data.get("content_type") if isinstance(data, dict) else None
        fetched_at = data.get("fetched_at") if isinstance(data, dict) else None
        if not isinstance(content_type, str):
            content_type = None
        if not isinstance(fetched_at, (int, float)):
            fetched_at = None
        return content_type, float(fetched_at) if fetched_at is not None else None
    except Exception:
        return None, None


def _write_meta(
    meta_path: Path,
    *,
    url: str,
    content_type: Optional[str],
    fetched_at: float,
    size: int,
    filename: str,
) -> None:
    meta = {
        "url": url,
        "filename": filename,
        "content_type": content_type,
        "fetched_at": fetched_at,
        "size": size,
    }
    meta_path.write_text(json.dumps(meta, ensure_ascii=False, indent=2), "utf-8")

