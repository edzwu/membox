from __future__ import annotations
from pathlib import Path
import tempfile

from membox.infra import JsonTaskRepository
from membox.application import AddTask, ListTasks, MarkTaskDone

def test_roundtrip():
    with tempfile.TemporaryDirectory() as td:
        repo = JsonTaskRepository(path=Path(td)/"tasks.json")
        t1 = AddTask(repo).execute("a")
        t2 = AddTask(repo).execute("b")
        tasks = ListTasks(repo).execute()
        assert [t.id for t in tasks] == [t1.id, t2.id]
        MarkTaskDone(repo).execute(t1.id)
        tasks2 = ListTasks(repo).execute()
        assert tasks2[0].status.value == "done"
