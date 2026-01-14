from __future__ import annotations
from dataclasses import dataclass
from datetime import date
from typing import Optional

from membox.domain import Task, TaskRepository, TaskStatus

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
