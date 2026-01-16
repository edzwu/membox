from __future__ import annotations
from dataclasses import dataclass
from datetime import date, datetime
from typing import Optional

from membox.domain import Task, TaskRepository, TaskStatus, Session, SessionRepository, SessionQueueKind

@dataclass
class ListTasks:
    repo: TaskRepository

    def execute(self, *, status: str | None = None) -> list[Task]:
        return self.repo.list_tasks(status=status)

@dataclass
class MarkTaskDone:
    repo: TaskRepository

    def execute(self, task_id: str) -> Task:
        task = self.repo.get(task_id)
        if task is None:
            raise ValueError(f"Task not found: {task_id}")
        updated = task.mark_done()
        self.repo.upsert(updated)
        return updated

@dataclass
class AddTask:
    repo: TaskRepository

    def execute(self, title: str, *, due: Optional[date] = None) -> Task:
        task = Task(id=self.repo.next_id(), title=title, status=TaskStatus.ACTIVE, due=due)
        self.repo.upsert(task)
        return task

@dataclass
class StartSession:
    repo: SessionRepository

    def execute(self, topic: str, *, start_at: Optional[datetime] = None) -> Session:
        session = Session(
            id=self.repo.next_id(),
            topic=topic,
            start_at=start_at or datetime.now(),
        )
        self.repo.upsert(session)
        return session

@dataclass
class ListSessions:
    repo: SessionRepository

    def execute(self, *, filter_text: Optional[str] = None) -> list[Session]:
        sessions = self.repo.list_sessions()
        if not filter_text:
            return sessions
        needle = filter_text.lower()
        out: list[Session] = []
        for s in sessions:
            if (
                needle in s.id.lower()
                or needle in s.topic.lower()
                or needle in s.start_at.isoformat(timespec="seconds").lower()
            ):
                out.append(s)
        return out

@dataclass
class AddSessionArtifact:
    repo: SessionRepository

    def execute(self, session_id: str, *, path: str) -> Session:
        session = self.repo.get(session_id)
        if session is None:
            raise ValueError(f"Session not found: {session_id}")
        updated = session.add_artifact(path)
        self.repo.upsert(updated)
        return updated

@dataclass
class AddSessionQueueItem:
    repo: SessionRepository

    def execute(self, session_id: str, *, item: str, kind: SessionQueueKind) -> Session:
        session = self.repo.get(session_id)
        if session is None:
            raise ValueError(f"Session not found: {session_id}")
        updated = session.add_queue_item(item, kind=kind)
        self.repo.upsert(updated)
        return updated
