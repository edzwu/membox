from __future__ import annotations
from dataclasses import dataclass, asdict
from typing import Any, Dict, List, Literal

@dataclass(frozen=True)
class Column:
    key: str
    label: str
    width: int | None = None  # optional width hint

@dataclass(frozen=True)
class Action:
    # An action is a UI-triggerable command back to the core host.
    label: str
    command: str
    args: Dict[str, Any]

@dataclass(frozen=True)
class TableView:
    type: Literal["table"]
    title: str
    columns: List[Column]
    rows: List[Dict[str, Any]]

@dataclass(frozen=True)
class ResultEnvelope:
    ok: bool
    view: TableView | None = None
    error: str | None = None
    row_actions: List[Action] | None = None

    def to_dict(self) -> Dict[str, Any]:
        return asdict(self)
