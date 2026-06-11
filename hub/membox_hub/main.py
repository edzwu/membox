from __future__ import annotations

import os

import uvicorn


def main() -> None:
    host = os.getenv("MEMBOX_HOST", "127.0.0.1")
    port = int(os.getenv("MEMBOX_PORT", "8080"))
    reload = os.getenv("MEMBOX_RELOAD", "1") not in ("0", "false", "False")
    uvicorn.run("hub.membox_hub.app:app", host=host, port=port, reload=reload)


if __name__ == "__main__":
    main()
