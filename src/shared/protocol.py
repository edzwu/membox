# shared/protocol.py
from __future__ import annotations

import json
from typing import Any, Dict, Optional, Union

from shared.errors import (
    INTERNAL_ERROR,
    INVALID_REQUEST,
    METHOD_NOT_FOUND,
    PARSE_ERROR,
)

Json = Union[Dict[str, Any], list, str, int, float, bool, None]


def dumps(obj: Json) -> bytes:
    # compact + utf-8
    return json.dumps(obj, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def loads(data: bytes) -> Json:
    return json.loads(data.decode("utf-8"))


def make_request(method: str, params: Any = None, req_id: Optional[Union[int, str]] = None) -> Dict[str, Any]:
    msg: Dict[str, Any] = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        msg["params"] = params
    if req_id is not None:
        msg["id"] = req_id
    return msg


def make_response(req_id: Union[int, str], result: Any) -> Dict[str, Any]:
    return {"jsonrpc": "2.0", "id": req_id, "result": result}


def make_error(req_id: Optional[Union[int, str]], code: int, message: str, data: Any = None) -> Dict[str, Any]:
    err: Dict[str, Any] = {"code": code, "message": message}
    if data is not None:
        err["data"] = data
    return {"jsonrpc": "2.0", "id": req_id, "error": err}


def is_request(msg: Any) -> bool:
    return isinstance(msg, dict) and msg.get("jsonrpc") == "2.0" and "method" in msg


def is_response(msg: Any) -> bool:
    return isinstance(msg, dict) and msg.get("jsonrpc") == "2.0" and ("result" in msg or "error" in msg)


def validate_request(msg: Dict[str, Any]) -> None:
    if msg.get("jsonrpc") != "2.0":
        raise ValueError("jsonrpc must be '2.0'")
    if not isinstance(msg.get("method"), str) or not msg["method"]:
        raise ValueError("method must be non-empty string")
    if "id" in msg and not isinstance(msg["id"], (int, str, type(None))):
        raise ValueError("id must be int|string|null")
