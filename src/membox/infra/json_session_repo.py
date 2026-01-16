from __future__ import annotations
import json
from datetime import datetime
from pathlib import Path
from typing import Optional

from membox.domain import Session
from .paths import membox_home

class JsonSessionRepository:
    def __init__(self, path: Optional[Path] = None) -> None:
        self.path = path or (membox_home() / "sessions.json")
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def _load(self) -> dict:
        if not self.path.exists():
            return {"sessions": [], "next_id": 1}
        data = json.loads(self.path.read_text(encoding="utf-8"))
        if "sessions" not in data:
            data["sessions"] = []
        if "next_id" not in data:
            data["next_id"] = 1
        return data

    def _dump(self, data: dict) -> None:
        self.path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")

    def list_sessions(self) -> list[Session]:
        data = self._load()
        out: list[Session] = []
        for s in data["sessions"]:
            start_s = s.get("start_at")
            start_at = datetime.fromisoformat(start_s) if start_s else datetime.now()
            out.append(Session(
                id=str(s["id"]),
                topic=s.get("topic", ""),
                start_at=start_at,
                artifacts=tuple(s.get("artifacts", [])),
                related_queue=tuple(s.get("related_queue", [])),
                misc_queue=tuple(s.get("misc_queue", [])),
            ))
        out.sort(key=lambda x: int(x.id))
        return out

    def get(self, session_id: str) -> Optional[Session]:
        for s in self.list_sessions():
            if s.id == str(session_id):
                return s
        return None

    def upsert(self, session: Session) -> None:
        data = self._load()
        sessions = data["sessions"]
        payload = {
            "id": str(session.id),
            "topic": session.topic,
            "start_at": session.start_at.isoformat(timespec="seconds"),
            "artifacts": list(session.artifacts),
            "related_queue": list(session.related_queue),
            "misc_queue": list(session.misc_queue),
        }
        for i, s in enumerate(sessions):
            if str(s["id"]) == str(session.id):
                sessions[i] = payload
                self._dump(data)
                return
        sessions.append(payload)
        self._dump(data)

    def next_id(self) -> str:
        data = self._load()
        nid = int(data.get("next_id", 1))
        data["next_id"] = nid + 1
        self._dump(data)
        return str(nid)
