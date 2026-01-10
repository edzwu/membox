# shared/errors.py
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional


@dataclass
class JsonRpcError(Exception):
    code: int
    message: str
    data: Optional[Any] = None


# JSON-RPC 2.0 standard-ish error codes (commonly used like LSP)
PARSE_ERROR = -32700
INVALID_REQUEST = -32600
METHOD_NOT_FOUND = -32601
INVALID_PARAMS = -32602
INTERNAL_ERROR = -32603
