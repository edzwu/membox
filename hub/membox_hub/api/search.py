from __future__ import annotations

import sqlite3

from fastapi import APIRouter, Depends, HTTPException

from hub.membox_hub.deps import get_conn
from membox_sdk.models import SearchRequest, SearchResponse, SearchHit

router = APIRouter()


@router.post("/search", response_model=SearchResponse)
def search(
    req: SearchRequest,
    conn: sqlite3.Connection = Depends(get_conn),
) -> SearchResponse:
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
