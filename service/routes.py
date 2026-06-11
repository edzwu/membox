from __future__ import annotations

import json
import sqlite3
import uuid
from datetime import datetime, timezone
from typing import List, Optional

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel, Field

from mcore.indexer import index_pdf, list_pdfs
from .deps import get_conn

router = APIRouter()


class OperationError(BaseModel):
    path: str
    error: str


class NoteIngestRequest(BaseModel):
    type: str = Field(default="note")
    backend: str = Field(..., description="Backend name")
    subdir: Optional[str] = Field(None, description="Subdirectory under backend root")
    source_url: Optional[str] = Field(None, description="Original source URL")
    title: str = Field(..., description="Note title")
    tags: List[str] = Field(default_factory=list)
    content: str = Field(..., description="Markdown or plain text content")


class NoteIngestResponse(BaseModel):
    uuid: str
    status: str


class IngestRequest(BaseModel):
    path: str = Field(..., description="PDF file or directory")
    glob: str = Field("*.pdf", description="Glob pattern when path is a directory")
    force: bool = Field(False, description="Force rebuild even if sha256 unchanged")
    min_chars: int = Field(200, ge=1, description="Merge short pages until reaching this size")


class IngestResult(BaseModel):
    doc_id: str
    source_path: str
    status: str
    chunks: Optional[int] = None


class IngestResponse(BaseModel):
    total: int
    indexed: int
    unchanged: int
    results: List[IngestResult]
    errors: List[OperationError] = Field(default_factory=list)


@router.post("/api/ingest", response_model=NoteIngestResponse)
def ingest_note(req: NoteIngestRequest, conn: sqlite3.Connection = Depends(get_conn)) -> NoteIngestResponse:
    note_uuid = str(uuid.uuid4())
    now = datetime.now(timezone.utc).isoformat()
    tags_json = json.dumps(req.tags, ensure_ascii=False)
    conn.execute(
        """
        INSERT INTO note (uuid, backend, subdir, source_url, title, tags, content, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (note_uuid, req.backend, req.subdir, req.source_url, req.title, tags_json, req.content, now, now),
    )
    conn.commit()
    return NoteIngestResponse(uuid=note_uuid, status="created")


@router.post("/ingest", response_model=IngestResponse)
def ingest(req: IngestRequest, conn: sqlite3.Connection = Depends(get_conn)) -> IngestResponse:
    try:
        pdfs = list_pdfs(req.path, glob_pat=req.glob)
    except FileNotFoundError as e:
        raise HTTPException(status_code=404, detail=str(e))

    indexed = unchanged = 0
    results: list[IngestResult] = []
    errors: list[OperationError] = []

    for p in pdfs:
        try:
            info = index_pdf(conn, p, force=req.force, min_chars=req.min_chars)
            if info["status"] == "indexed":
                indexed += 1
            else:
                unchanged += 1
            results.append(IngestResult(**info))
        except Exception as e:  # noqa: BLE001
            errors.append(OperationError(path=p, error=str(e)))

    return IngestResponse(
        total=len(pdfs),
        indexed=indexed,
        unchanged=unchanged,
        results=results,
        errors=errors,
    )


class SearchRequest(BaseModel):
    query: str
    topk: int = Field(10, ge=1, le=100, description="Number of results to return")
    doc: Optional[str] = Field(None, description="Restrict to doc path or doc_id")
    path_prefix: Optional[str] = Field(None, description="Restrict to docs whose path starts with this prefix")
    snippet_tokens: int = Field(24, ge=4, description="Token count for snippet()")
    max_chars: int = Field(400, ge=1, description="Max chars of chunk text to return")


class SearchHit(BaseModel):
    source_path: str
    page_start: Optional[int]
    page_end: Optional[int]
    chunk_id: str
    score: float
    snippet: str
    text: str


class SearchResponse(BaseModel):
    query: str
    hits: List[SearchHit]


@router.post("/search", response_model=SearchResponse)
def search(req: SearchRequest, conn: sqlite3.Connection = Depends(get_conn)) -> SearchResponse:
    q = req.query.strip()
    if not q:
        raise HTTPException(status_code=400, detail="Query cannot be empty")

    where = []
    params: list[object] = []

    if req.doc:
        row = conn.execute(
            "SELECT doc_id FROM doc WHERE source_path=? OR doc_id=?",
            (req.doc, req.doc),
        ).fetchone()
        if not row:
            raise HTTPException(status_code=404, detail=f"No such doc: {req.doc}")
        where.append("f.doc_id = ?")
        params.append(row["doc_id"])

    if req.path_prefix:
        where.append("d.source_path LIKE ?")
        params.append(req.path_prefix.rstrip("/") + "%")

    where_sql = (" AND " + " AND ".join(where)) if where else ""

    sql = f"""
    SELECT d.source_path,
           c.page_start, c.page_end, c.chunk_id,
           bm25(chunk_fts) AS bm25_score,
           snippet(chunk_fts, 0, '[', ']', '…', ?) AS snip,
           substr(c.text, 1, ?) AS text_preview
    FROM chunk_fts f
    JOIN doc d ON d.doc_id = f.doc_id
    JOIN chunk c ON c.chunk_id = f.chunk_id
    WHERE chunk_fts MATCH ? {where_sql}
    ORDER BY bm25_score
    LIMIT ?
    """

    try:
        rows = conn.execute(sql, (req.snippet_tokens, req.max_chars, q, *params, req.topk)).fetchall()
    except sqlite3.Error as e:
        raise HTTPException(status_code=400, detail=str(e))

    hits: list[SearchHit] = []
    for r in rows:
        bm25_score = float(r["bm25_score"]) if r["bm25_score"] is not None else 0.0
        score = 1.0 / (1.0 + bm25_score)
        hits.append(
            SearchHit(
                source_path=r["source_path"],
                page_start=r["page_start"],
                page_end=r["page_end"],
                chunk_id=r["chunk_id"],
                score=score,
                snippet=r["snip"],
                text=r["text_preview"],
            )
        )

    return SearchResponse(query=q, hits=hits)


class ReindexRequest(BaseModel):
    path: Optional[str] = Field(None, description="If set, reindex this file/dir. Otherwise reindex existing docs")
    glob: str = Field("*.pdf", description="Glob pattern when path is a directory")
    force: bool = Field(True, description="Force rebuild existing entries")
    min_chars: int = Field(200, ge=1)


class ReindexResponse(BaseModel):
    total: int
    indexed: int
    unchanged: int
    results: List[IngestResult]
    errors: List[OperationError] = Field(default_factory=list)


@router.post("/reindex", response_model=ReindexResponse)
def reindex(req: ReindexRequest, conn: sqlite3.Connection = Depends(get_conn)) -> ReindexResponse:
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
    results: list[IngestResult] = []
    errors: list[OperationError] = []

    for p in targets:
        try:
            info = index_pdf(conn, p, force=req.force, min_chars=req.min_chars)
            if info["status"] == "indexed":
                indexed += 1
            else:
                unchanged += 1
            results.append(IngestResult(**info))
        except Exception as e:  # noqa: BLE001
            errors.append(OperationError(path=p, error=str(e)))

    return ReindexResponse(
        total=len(targets),
        indexed=indexed,
        unchanged=unchanged,
        results=results,
        errors=errors,
    )
