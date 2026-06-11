from __future__ import annotations

from typing import List, Optional

from pydantic import BaseModel, Field


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


class PdfIngestRequest(BaseModel):
    path: str = Field(..., description="PDF file or directory")
    glob: str = Field("*.pdf", description="Glob pattern when path is a directory")
    force: bool = Field(False, description="Force rebuild even if sha256 unchanged")
    min_chars: int = Field(200, ge=1, description="Merge short pages until reaching this size")


class PdfIngestResult(BaseModel):
    doc_id: str
    source_path: str
    status: str
    chunks: Optional[int] = None


class PdfIngestResponse(BaseModel):
    total: int
    indexed: int
    unchanged: int
    results: List[PdfIngestResult]
    errors: List[OperationError] = Field(default_factory=list)


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


class ReindexRequest(BaseModel):
    path: Optional[str] = Field(None, description="If set, reindex this file/dir. Otherwise reindex existing docs")
    glob: str = Field("*.pdf", description="Glob pattern when path is a directory")
    force: bool = Field(True, description="Force rebuild existing entries")
    min_chars: int = Field(200, ge=1)


class ReindexResponse(BaseModel):
    total: int
    indexed: int
    unchanged: int
    results: List[PdfIngestResult]
    errors: List[OperationError] = Field(default_factory=list)
