# membox-doc — pi agent extension

Wraps the `mm doc` CLI as a [pi](https://github.com/earendil-works/pi) coding-agent
extension: the agent can resolve, search, read, create, rename, and trash membox
documents, and — when toggled on — short IDs like `11e8` in your messages are
auto-resolved to the concrete file before the model sees them.

## Install

The source lives here; pi discovers extensions from `~/.pi/agent/extensions/`.
Link it so the repo stays the single source of truth:

```bash
ln -s "$(pwd)/membox-doc.ts" ~/.pi/agent/extensions/membox-doc.ts
```

Then `/reload` in pi (or restart). The `mm` binary is resolved from
`$MEMBOX_MM` if set, else `~/repo/membox/bin/mm` — build it first:
`cd ~/repo/membox && go build -o bin/mm ./cmd/mm`.

## Usage

| Action | How |
|---|---|
| Toggle reference resolution | `ctrl+shift+m` or `/membox-doc` |
| Status indicator | `◈ membox` in the status bar when ON |

While ON, a message such as "把 membox 的 11e8 列入今天的任务" is augmented
with the resolved file context (`11e8 → membox-code-refactor.md`), so the
model knows exactly which file is meant.

## Tools

| Tool | Backing CLI | Notes |
|---|---|---|
| `membox_doc_resolve` | `mm doc show --json` | short ID / UUID / path → file |
| `membox_doc_search` | `mm doc search --json` | full-text over titles + bodies |
| `membox_doc_cat` | `mm doc cat` | read full content (truncatable) |
| `membox_doc_list` | `mm doc list --json` | optional title/path filter |
| `membox_doc_create` | write file + `mm path scan` | any extension (not just .md) |
| `membox_doc_rename` | `mm doc rename` | keeps the UUID |
| `membox_doc_delete` | `mm doc delete` | soft delete (trash) + confirm |

All tools talk JSON to the `mm` CLI, so any new `mm doc` subcommand can be
exposed by adding a `pi.registerTool` wrapper here.
