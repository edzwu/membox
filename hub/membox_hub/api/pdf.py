from __future__ import annotations

import sqlite3

from fastapi import APIRouter, Depends, HTTPException

from hub.membox_hub.deps import get_conn
from mcore.indexer import index_pdf, list_pdfs
from membox_sdk.models import (
    PdfIngestRequest,
    PdfIngestResponse,
    PdfIngestResult,
    OperationError,
    ReindexRequest,
    ReindexResponse,
)

router = APIRouter()


@router.post("/ingest", response_model=PdfIngestResponse)
def ingest_pdf_endpoint(
    req: PdfIngestRequest,
    conn: sqlite3.Connection = Depends(get_conn),
) -> PdfIngestResponse:
    try:
        pdfs = list_pdfs(req.path, glob_pat=req.glob)
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e))

    indexed = unchanged = 0
    results: list[PdfIngestResult] = []
    errors: list[OperationError] = []

    for p in pdfs:
        try:
            info = index_pdf(conn, p, force=req.force, min_chars=req.min_chars)
            if info["status"] == "indexed":
                indexed += 1
            else:
                unchanged += 1
            results.append(PdfIngestResult(**info))
        except Exception as e:  # noqa: BLE001
            errors.append(OperationError(path=p, error=str(e)))

    return PdfIngestResponse(
        total=len(pdfs),
        indexed=indexed,
        unchanged=unchanged,
        results=results,
        errors=errors,
    )


@router.post("/reindex", response_model=ReindexResponse)
def reindex(
    req: ReindexRequest,
    conn: sqlite3.Connection = Depends(get_conn),
) -> ReindexResponse:
    if req.path:
        try:
            targets = list_pdfs(req.path, glob_pat=req.glob)
        except FileNotFoundError as e:
            raise HTTPException(status_code=404, detail=str(e))
    else:
        rows = conn.execute("SELECT source_path FROM doc ORDER BY created_at").fetchall()
        targets = [r["source_path"] for r in rows]
        if not targets:
            raise HTTPException(status_code=404, detail="No docs found to reindex")

    indexed = unchanged = 0
    results: list[PdfIngestResult] = []
    errors: list[OperationError] = []

    for p in targets:
        try:
            info = index_pdf(conn, p, force=req.force, min_chars=req.min_chars)
            if info["status"] == "indexed":
                indexed += 1
            else:
                unchanged += 1
            results.append(PdfIngestResult(**info))
        except Exception as e:  # noqa: BLE001
            errors.append(OperationError(path=p, error=str(e)))

    return ReindexResponse(
        total=len(targets),
        indexed=indexed,
        unchanged=unchanged,
        results=results,
        errors=errors,
    )
