from __future__ import annotations
from dataclasses import dataclass
from enum import StrEnum
from datetime import date
from typing import Optional

class TaskStatus(StrEnum):
    ACTIVE = "active"
    DONE = "done"

@dataclass(frozen=True)
class Task:
    id: str
    title: str
    status: TaskStatus = TaskStatus.ACTIVE
    due: Optional[date] = None

    def mark_done(self) -> "Task":
        return Task(id=self.id, title=self.title, status=TaskStatus.DONE, due=self.due)
