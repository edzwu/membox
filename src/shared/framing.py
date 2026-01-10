# shared/framing.py
from __future__ import annotations

from typing import BinaryIO, Dict


class FramingError(Exception):
    pass


_HEADER_SEP = b"\r\n"
_HEADER_END = b"\r\n\r\n"


def _read_headers(stream: BinaryIO) -> Dict[str, str]:
    """
    Read LSP-style headers:
      Content-Length: N\r\n
      ...\r\n
      \r\n
    """
    headers: Dict[str, str] = {}
    # read lines until blank line
    while True:
        line = stream.readline()
        if not line:
            raise EOFError("EOF while reading headers")

        # header section end
        if line in (b"\r\n", b"\n"):
            break

        # tolerate "\n" endings
        line = line.rstrip(b"\r\n")
        if b":" not in line:
            raise FramingError(f"Bad header line: {line!r}")

        k, v = line.split(b":", 1)
        headers[k.decode("ascii", "strict").strip().lower()] = v.decode("ascii", "strict").strip()

    return headers


def _read_exact(stream: BinaryIO, n: int) -> bytes:
    buf = bytearray()
    while len(buf) < n:
        chunk = stream.read(n - len(buf))
        if not chunk:
            raise EOFError(f"EOF while reading body: need {n}, got {len(buf)}")
        buf += chunk
    return bytes(buf)


def read_message(stream: BinaryIO) -> bytes:
    """
    Returns the raw body bytes (JSON bytes) of one framed message.
    """
    headers = _read_headers(stream)
    if "content-length" not in headers:
        raise FramingError("Missing Content-Length header")

    try:
        length = int(headers["content-length"])
    except ValueError as e:
        raise FramingError(f"Bad Content-Length: {headers['content-length']!r}") from e

    if length < 0:
        raise FramingError("Negative Content-Length")

    return _read_exact(stream, length)


def write_message(stream: BinaryIO, body: bytes) -> None:
    header = f"Content-Length: {len(body)}\r\n\r\n".encode("ascii")
    stream.write(header)
    stream.write(body)
    stream.flush()
