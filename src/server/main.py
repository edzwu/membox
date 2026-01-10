# server/main.py
from __future__ import annotations

import logging
import sys
import traceback
from typing import Any, Optional, Union

from shared.errors import (
    INTERNAL_ERROR,
    INVALID_REQUEST,
    JsonRpcError,
    METHOD_NOT_FOUND,
    PARSE_ERROR,
)
from shared.framing import FramingError, read_message, write_message
from shared.protocol import (
    dumps,
    is_request,
    loads,
    make_error,
    make_response,
    validate_request,
)
from server.handlers import default_registry

log = logging.getLogger("server")


JsonId = Union[int, str]


def _safe_send(msg: dict) -> None:
    write_message(sys.stdout.buffer, dumps(msg))


def main() -> int:
    logging.basicConfig(stream=sys.stderr, level=logging.INFO)

    reg = default_registry()
    log.info("server started (stdio)")

    while True:
        try:
            body = read_message(sys.stdin.buffer)
        except EOFError:
            log.info("stdin EOF, exiting")
            return 0
        except FramingError as e:
            log.error("framing error: %s", e)
            # framing error => cannot trust id, use null
            _safe_send(make_error(None, INVALID_REQUEST, f"Framing error: {e}"))
            continue
        except Exception as e:
            log.error("unexpected read error: %s", e)
            _safe_send(make_error(None, INTERNAL_ERROR, "Read failure"))
            continue

        # decode json
        try:
            msg = loads(body)
        except Exception as e:
            log.error("parse error: %s", e)
            _safe_send(make_error(None, PARSE_ERROR, "Parse error"))
            continue

        if not is_request(msg):
            _safe_send(make_error(None, INVALID_REQUEST, "Invalid request"))
            continue

        # validate request fields
        req_id: Optional[JsonId] = msg.get("id")  # may be absent (notification)
        method = msg.get("method")
        params = msg.get("params", None)

        try:
            validate_request(msg)
        except Exception as e:
            _safe_send(make_error(req_id, INVALID_REQUEST, f"Invalid request: {e}"))
            continue

        handler = reg.get(method)
        if handler is None:
            # only reply if request has id; notification => no response
            if req_id is not None:
                _safe_send(make_error(req_id, METHOD_NOT_FOUND, f"Method not found: {method}"))
            continue

        try:
            result = handler(params)
            if req_id is not None:
                _safe_send(make_response(req_id, result))
        except JsonRpcError as e:
            if req_id is not None:
                _safe_send(make_error(req_id, e.code, e.message, e.data))
        except Exception:
            log.error("handler crashed:\n%s", traceback.format_exc())
            if req_id is not None:
                _safe_send(make_error(req_id, INTERNAL_ERROR, "Internal error"))

    # unreachable
    # return 0


if __name__ == "__main__":
    raise SystemExit(main())
