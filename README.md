# Membox v0.1 (Neovim as UI client + Python core)

This repo is a **v0.1 reference project** that treats **Neovim as a replaceable UI client** (like VS Code UI),
and keeps all business logic in a Python **core host** (`memboxd`) with a small, stable **protocol envelope**.

## What you get

- Python core with DDD-ish layers:
  - `domain/` (entities + repository interface)
  - `application/` (use-cases)
  - `infra/` (JSON-file repository)
  - `protocol/` (ResultEnvelope: table view + actions)
  - `apps/` (CLI adapter: `memboxd`)
- Research sessions with queues + artifacts:
  - `session start <topic>` creates a session with a start timestamp
  - `session file add` tracks edited files as session artifacts
  - `session queue add` collects related vs misc ideas
- External CLI bridge (calx/caly, etc.):
  - `external <command> [args...]` runs a CLI and renders stdout as a table
- Thin Neovim UI:
  - `:Membox` opens a "pseudo-terminal" prompt + a result panel.
  - Type `task ls` → table.
  - Press `d` on a row → mark done (refresh).

## Quick start

### 1) Install in a venv

```bash
python -m venv .venv
source .venv/bin/activate
pip install -e .
```

### 2) Use `memboxd`

```bash
memboxd task add "Read chapter 4"
memboxd task add "Implement JSON-RPC initialize"
memboxd task ls
memboxd task done 1

memboxd session start "论文阅读：LLM 对齐"
memboxd session file add 1 ./docs/notes.md
memboxd session queue add 1 related "补充阅读 InstructGPT"
memboxd session queue add 1 misc "给同事回邮件"
memboxd session ls --filter 2026-01-16-16:00:00

memboxd external calx today
memboxd external caly week
```

Data is stored in:
- `$MEMBOX_HOME/tasks.json` if `MEMBOX_HOME` is set
- otherwise `~/.membox/tasks.json`
- sessions in `$MEMBOX_HOME/sessions.json` (or `~/.membox/sessions.json`)

Membox focuses on session state + UI protocol. Schedule data comes from external CLIs
invoked via `external`, so your Neovim client only needs to send commands and render
the envelope response.

### 3) Use the Neovim client

Add this directory to runtimepath:

```vim
" replace with your local path
set rtp+=/path/to/membox_v0_1/clients/nvim
```

Restart nvim, then:

```vim
:Membox
```

In prompt:
- `task ls`
- `task add "title"`
- `task done 1`

Result panel keys:
- `d` mark done (selected row)
- `r` refresh last command
- `q` close

## Why this scales

- `clients/nvim/` is replaceable. Future: Textual-based TUI editor.
- The stable boundary is: **command line string in → structured envelope out**.
