from __future__ import annotations

import sqlite3

from fastapi import APIRouter, Depends, HTTPException

from hub.membox_hub.deps import get_conn
from hub.membox_hub.services.note import save_note
from membox_sdk.models import NoteIngestRequest, NoteIngestResponse

router = APIRouter()


@router.post("/api/ingest", response_model=NoteIngestResponse)
def ingest_note(
    req: NoteIngestRequest,
    conn: sqlite3.Connection = Depends(get_conn),
) -> NoteIngestResponse:
    try:
        return save_note(req, conn)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e))
