from __future__ import annotations

import json
import re
import sqlite3
import uuid
from datetime import datetime, timezone
from pathlib import Path

from membox_sdk.models import NoteIngestRequest, NoteIngestResponse
from hub.membox_hub.config import get_backend_root


def _slugify(text: str) -> str:
    """简单的中文/英文 slug 生成。"""
    text = text.strip().lower()
    text = re.sub(r"[^\w\s-]", "", text)
    text = re.sub(r"[-\s]+", "-", text)
    return text[:50]


def save_note(req: NoteIngestRequest, conn: sqlite3.Connection) -> NoteIngestResponse:
    note_uuid = str(uuid.uuid4())
    now = datetime.now(timezone.utc).isoformat()

    backend_root = get_backend_root(req.backend)
    subdir = Path(req.subdir or "")
    slug = _slugify(req.title) or "untitled"
    filename = f"{now[:10]}-{slug}.md"
    target_dir = backend_root / subdir
    target_path = target_dir / filename

    # 组装带 frontmatter 的 Markdown
    tags_json = json.dumps(req.tags, ensure_ascii=False)
    md_content = f"""---
uuid: {note_uuid}
title: {req.title}
backend: {req.backend}
subdir: {req.subdir or ""}
source_url: {req.source_url or ""}
tags: {tags_json}
created_at: {now}
---

{req.content}
"""

    # 写文件（事务前先写，失败可回滚数据库）
    target_dir.mkdir(parents=True, exist_ok=True)
    target_path.write_text(md_content, encoding="utf-8")

    # 写数据库
    conn.execute(
        """
        INSERT INTO note (uuid, backend, subdir, source_url, title, tags, content, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            note_uuid,
            req.backend,
            req.subdir,
            req.source_url,
            req.title,
            tags_json,
            req.content,
            now,
            now,
        ),
    )
    conn.commit()

    return NoteIngestResponse(uuid=note_uuid, status="created")
