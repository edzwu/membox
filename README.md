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
```

Data is stored in:
- `$MEMBOX_HOME/tasks.json` if `MEMBOX_HOME` is set
- otherwise `~/.membox/tasks.json`

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
