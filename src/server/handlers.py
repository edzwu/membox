# server/handlers.py
from __future__ import annotations

from typing import Any, Callable, Dict, Optional

from server.extensions.loader import load_extensions


Handler = Callable[[Any], Any]


class HandlerRegistry:
    def __init__(self) -> None:
        self._handlers: Dict[str, Handler] = {}

    def register(self, method: str, fn: Handler) -> None:
        if not method or not isinstance(method, str):
            raise ValueError("method must be a non-empty string")
        self._handlers[method] = fn

    def get(self, method: str) -> Optional[Handler]:
        return self._handlers.get(method)


def default_registry() -> HandlerRegistry:
    reg = HandlerRegistry()

    def ping(params: Any) -> Any:
        # params 可选，原样回显也行；M0 只回 pong
        return "pong"

    reg.register("ping", ping)
    load_extensions(reg)
    return reg
