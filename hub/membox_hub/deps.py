from __future__ import annotations

import os
import sqlite3
from typing import Iterator

from dotenv import load_dotenv
from fastapi import Depends

from mcore.db import connect, init_db
from mcore.util import norm_path

load_dotenv()


def get_db_path() -> str:
    return norm_path(os.getenv("MEMBOX_DB_PATH", "data/membox.sqlite"))


def get_conn(db_path: str = Depends(get_db_path)) -> Iterator[sqlite3.Connection]:
    conn = connect(db_path)
    init_db(conn)
    try:
        yield conn
    finally:
        conn.close()
