from __future__ import annotations
from dataclasses import dataclass
from enum import StrEnum
from datetime import date, datetime
from typing import Optional

class TaskStatus(StrEnum):
    ACTIVE = "active"
    DONE = "done"

class SessionQueueKind(StrEnum):
    RELATED = "related"
    MISC = "misc"

@dataclass(frozen=True)
class Task:
    id: str
    title: str
    status: TaskStatus = TaskStatus.ACTIVE
    due: Optional[date] = None

    def mark_done(self) -> "Task":
        return Task(id=self.id, title=self.title, status=TaskStatus.DONE, due=self.due)

@dataclass(frozen=True)
class Session:
    id: str
    topic: str
    start_at: datetime
    artifacts: tuple[str, ...] = ()
    related_queue: tuple[str, ...] = ()
    misc_queue: tuple[str, ...] = ()

    def add_artifact(self, path: str) -> "Session":
        if path in self.artifacts:
            return self
        return Session(
            id=self.id,
            topic=self.topic,
            start_at=self.start_at,
            artifacts=self.artifacts + (path,),
            related_queue=self.related_queue,
            misc_queue=self.misc_queue,
        )

    def add_queue_item(self, item: str, *, kind: SessionQueueKind) -> "Session":
        if kind == SessionQueueKind.RELATED:
            return Session(
                id=self.id,
                topic=self.topic,
                start_at=self.start_at,
                artifacts=self.artifacts,
                related_queue=self.related_queue + (item,),
                misc_queue=self.misc_queue,
            )
        return Session(
            id=self.id,
            topic=self.topic,
            start_at=self.start_at,
            artifacts=self.artifacts,
            related_queue=self.related_queue,
            misc_queue=self.misc_queue + (item,),
        )
