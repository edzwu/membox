from __future__ import annotations
import json
from datetime import date
from pathlib import Path
from typing import Optional

from membox.domain import Task, TaskStatus
from .paths import membox_home

class JsonTaskRepository:
    def __init__(self, path: Optional[Path] = None) -> None:
        self.path = path or (membox_home() / "tasks.json")
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def _load(self) -> dict:
        if not self.path.exists():
            return {"tasks": [], "next_id": 1}
        data = json.loads(self.path.read_text(encoding="utf-8"))
        if "tasks" not in data:
            data["tasks"] = []
        if "next_id" not in data:
            data["next_id"] = 1
        return data

    def _dump(self, data: dict) -> None:
        self.path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")

    def list_tasks(self, *, status: str | None = None) -> list[Task]:
        data = self._load()
        out: list[Task] = []
        for t in data["tasks"]:
            st = TaskStatus(t.get("status", "active"))
            if status and st != TaskStatus(status):
                continue
            due_s = t.get("due")
            due = date.fromisoformat(due_s) if due_s else None
            out.append(Task(id=str(t["id"]), title=t["title"], status=st, due=due))
        out.sort(key=lambda x: int(x.id))
        return out

    def get(self, task_id: str) -> Optional[Task]:
        for t in self.list_tasks():
            if t.id == str(task_id):
                return t
        return None

    def upsert(self, task: Task) -> None:
        data = self._load()
        tasks = data["tasks"]
        for i, t in enumerate(tasks):
            if str(t["id"]) == str(task.id):
                tasks[i] = {
                    "id": str(task.id),
                    "title": task.title,
                    "status": str(task.status),
                    "due": task.due.isoformat() if task.due else None,
                }
                self._dump(data)
                return
        tasks.append({
            "id": str(task.id),
            "title": task.title,
            "status": str(task.status),
            "due": task.due.isoformat() if task.due else None,
        })
        self._dump(data)

    def next_id(self) -> str:
        data = self._load()
        nid = int(data.get("next_id", 1))
        data["next_id"] = nid + 1
        self._dump(data)
        return str(nid)
