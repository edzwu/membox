from __future__ import annotations

import importlib
import logging
import pkgutil
from types import ModuleType
from typing import Any, Callable, Protocol

log = logging.getLogger("server.extensions")


class _Registry(Protocol):
    def register(self, method: str, fn: Callable[[Any], Any]) -> None: ...


def load_extensions(reg: _Registry, package: str = "server.extensions") -> None:
    """
    Load all modules in `package` and call `register(reg)` if present.

    This keeps extension wiring simple while we iterate on the framework.
    """
    try:
        pkg = importlib.import_module(package)
    except Exception:
        log.exception("failed to import extensions package: %s", package)
        return

    pkg_path = getattr(pkg, "__path__", None)
    if pkg_path is None:
        return

    for mod in pkgutil.iter_modules(pkg_path, pkg.__name__ + "."):
        if mod.ispkg:
            continue
        try:
            module = importlib.import_module(mod.name)
        except Exception:
            log.exception("failed to import extension: %s", mod.name)
            continue
        _maybe_register(module, reg)


def _maybe_register(module: ModuleType, reg: _Registry) -> None:
    fn = getattr(module, "register", None)
    if not callable(fn):
        return
    try:
        register_fn: Callable[[_Registry], None] = fn
        register_fn(reg)
        log.info("loaded extension: %s", module.__name__)
    except Exception:
        log.exception("extension register() crashed: %s", module.__name__)
