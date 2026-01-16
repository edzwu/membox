from __future__ import annotations
import argparse
import json
import shlex
import subprocess
import sys

from membox.application import (
    AddTask,
    ListTasks,
    MarkTaskDone,
    StartSession,
    ListSessions,
    AddSessionArtifact,
    AddSessionQueueItem,
)
from membox.domain import SessionQueueKind
from membox.infra import JsonTaskRepository, JsonSessionRepository
from membox.protocol import ResultEnvelope, TableView, Column, Action

def _repo() -> JsonTaskRepository:
    return JsonTaskRepository()

def _session_repo() -> JsonSessionRepository:
    return JsonSessionRepository()

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

def _sessions_table(sessions):
    cols = [
        Column(key="id", label="ID", width=4),
        Column(key="topic", label="Topic", width=28),
        Column(key="start_at", label="Start", width=19),
        Column(key="artifacts", label="Files", width=6),
        Column(key="related", label="Related", width=8),
        Column(key="misc", label="Misc", width=6),
    ]
    rows = [{
        "id": s.id,
        "topic": s.topic,
        "start_at": s.start_at.isoformat(timespec="seconds"),
        "artifacts": len(s.artifacts),
        "related": len(s.related_queue),
        "misc": len(s.misc_queue),
    } for s in sessions]
    view = TableView(type="table", title="Sessions", columns=cols, rows=rows)
    return ResultEnvelope(ok=True, view=view)

def _text_table(title: str, text: str) -> ResultEnvelope:
    cols = [Column(key="line", label="Output")]
    rows = [{"line": line} for line in text.splitlines()] or [{"line": ""}]
    view = TableView(type="table", title=title, columns=cols, rows=rows)
    return ResultEnvelope(ok=True, view=view)

def _run_external(cmd: list[str]) -> ResultEnvelope:
    if not cmd:
        return ResultEnvelope(ok=False, error="usage: external <command> [args...]")
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    except FileNotFoundError:
        return ResultEnvelope(ok=False, error=f"external command not found: {cmd[0]}")
    if proc.returncode != 0:
        err = proc.stderr.strip() or f"external command failed: {cmd[0]}"
        return ResultEnvelope(ok=False, error=err)
    out = proc.stdout.rstrip("\n")
    title = f"External: {' '.join(cmd)}"
    return _text_table(title, out)

def _exec(tokens: list[str]) -> dict:
    if not tokens:
        return ResultEnvelope(ok=False, error="empty command").to_dict()

    if tokens[0] not in {"task", "session", "external"}:
        return ResultEnvelope(ok=False, error=f"unknown top-level: {tokens[0]}").to_dict()
    if len(tokens) == 1:
        return ResultEnvelope(ok=False, error="usage: task|session|external <subcommand>").to_dict()

    if tokens[0] == "task":
        repo = _repo()
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

    if tokens[0] == "external":
        env = _run_external(tokens[1:])
        return env.to_dict()

    repo = _session_repo()
    sub = tokens[1]
    if sub == "start":
        if len(tokens) < 3:
            return ResultEnvelope(ok=False, error="usage: session start <topic>").to_dict()
        topic = " ".join(tokens[2:])
        session = StartSession(repo).execute(topic=topic)
        return _sessions_table([session]).to_dict()

    if sub == "ls":
        filter_text = None
        if "--filter" in tokens:
            i = tokens.index("--filter")
            if i + 1 < len(tokens):
                filter_text = tokens[i + 1]
        sessions = ListSessions(repo).execute(filter_text=filter_text)
        return _sessions_table(sessions).to_dict()

    if sub == "file":
        if len(tokens) < 5 or tokens[2] != "add":
            return ResultEnvelope(
                ok=False,
                error="usage: session file add <id> <path>",
            ).to_dict()
        session_id = tokens[3]
        path = " ".join(tokens[4:])
        if not path:
            return ResultEnvelope(ok=False, error="usage: session file add <id> <path>").to_dict()
        session = AddSessionArtifact(repo).execute(session_id=session_id, path=path)
        return _sessions_table([session]).to_dict()

    if sub == "queue":
        if len(tokens) < 6 or tokens[2] != "add":
            return ResultEnvelope(
                ok=False,
                error="usage: session queue add <id> <related|misc> <item>",
            ).to_dict()
        session_id = tokens[3]
        kind_raw = tokens[4]
        item = " ".join(tokens[5:])
        if kind_raw == "related":
            kind = SessionQueueKind.RELATED
        elif kind_raw == "misc":
            kind = SessionQueueKind.MISC
        else:
            return ResultEnvelope(
                ok=False,
                error="usage: session queue add <id> <related|misc> <item>",
            ).to_dict()
        session = AddSessionQueueItem(repo).execute(session_id=session_id, item=item, kind=kind)
        return _sessions_table([session]).to_dict()

    return ResultEnvelope(ok=False, error=f"unknown session subcommand: {sub}").to_dict()

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
