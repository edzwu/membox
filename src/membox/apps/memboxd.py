from __future__ import annotations
import argparse
import json
import shlex
import sys

from membox.application import AddTask, ListTasks, MarkTaskDone
from membox.infra import JsonTaskRepository
from membox.protocol import ResultEnvelope, TableView, Column, Action

def _repo() -> JsonTaskRepository:
    return JsonTaskRepository()

def _tasks_table(tasks):
    cols = [
        Column(key="id", label="ID", width=4),
        Column(key="title", label="Title", width=34),
        Column(key="status", label="Status", width=8),
        Column(key="due", label="Due", width=10),
    ]
    rows = [{
        "id": t.id,
        "title": t.title,
        "status": str(t.status),
        "due": t.due.isoformat() if t.due else "",
    } for t in tasks]
    view = TableView(type="table", title="Tasks", columns=cols, rows=rows)
    row_actions = [Action(label="Done", command="task.done", args={"id": "$row.id"})]
    return ResultEnvelope(ok=True, view=view, row_actions=row_actions)

def _exec(tokens: list[str]) -> dict:
    repo = _repo()
    if not tokens:
        return ResultEnvelope(ok=False, error="empty command").to_dict()

    if tokens[0] != "task":
        return ResultEnvelope(ok=False, error=f"unknown top-level: {tokens[0]}").to_dict()
    if len(tokens) == 1:
        return ResultEnvelope(ok=False, error="usage: task <ls|add|done>").to_dict()

    sub = tokens[1]
    if sub == "ls":
        status = None
        if "--status" in tokens:
            i = tokens.index("--status")
            if i + 1 < len(tokens):
                status = tokens[i + 1]
        tasks = ListTasks(repo).execute(status=status)
        return _tasks_table(tasks).to_dict()

    if sub == "add":
        if len(tokens) < 3:
            return ResultEnvelope(ok=False, error="usage: task add <title>").to_dict()
        title = " ".join(tokens[2:])
        t = AddTask(repo).execute(title=title)
        return _tasks_table([t]).to_dict()

    if sub == "done":
        if len(tokens) < 3:
            return ResultEnvelope(ok=False, error="usage: task done <id>").to_dict()
        MarkTaskDone(repo).execute(task_id=tokens[2])
        tasks = ListTasks(repo).execute()
        return _tasks_table(tasks).to_dict()

    return ResultEnvelope(ok=False, error=f"unknown task subcommand: {sub}").to_dict()

def _handle_line(line: str) -> dict:
    return _exec(shlex.split(line))

def main(argv: list[str] | None = None) -> None:
    p = argparse.ArgumentParser(prog="memboxd")
    p.add_argument("--stdin", action="store_true", help="Read JSON from stdin: {line: '...'}")
    p.add_argument("rest", nargs=argparse.REMAINDER)
    args = p.parse_args(argv)

    try:
        if args.stdin:
            obj = json.loads(sys.stdin.read() or "{}")
            line = obj.get("line", "")
        else:
            line = " ".join(args.rest).strip()
        out = _handle_line(line)
    except Exception as e:
        out = ResultEnvelope(ok=False, error=str(e)).to_dict()

    sys.stdout.write(json.dumps(out, ensure_ascii=False))

if __name__ == "__main__":
    main()
