# membox

Local document identity and search for Markdown.

membox keeps Markdown files as the content authority and stores stable document UUIDs, current locations, metadata, and an FTS5 index in local SQLite. It does not copy or rewrite source Markdown.

## Build

Go 1.26+ is required.

```bash
make build
# produces bin/mm
```

Direct Go build also works:

```bash
go build -o mm ./cmd/mm
```

## Quick start

```bash
./mm path add ~/notes
./mm path list
./mm doc search "flash attention"
./mm doc list
./mm doc show <document-id>
./mm doc cat <document-id>
./mm doc edit <document-id>
./mm doc rename <document-id> <new-filename>
./mm note new "Online Softmax Intuition" --from <document-id>
./mm topic create attention
./mm topic add <topic-id> <document-id>
./mm link add <from-id> <to-id>
./mm link list <document-id>
./mm link graph <document-id>
./mm trash list
./mm trash restore <document-id>
./mm trash purge --all
./mm path scan
./mm path scan --timestamp=git
./mm index status
./mm
```

The default database is `~/.membox/membox.db`. Override the home with `MEMBOX_HOME` or `--home`.

## Identity model

```text
Document UUID   stable logical identity
Filesystem path current location
SHA-256         current byte fingerprint
SQLite FTS5     rebuildable search projection
```

External edits are reflected after `mm path scan`. A conservative scanner preserves identity for unique filesystem-file-key and exact-hash renames; ambiguous matches are never automatically merged.

## Document dates from Git

A scan initially uses filesystem modification time as the document creation and modification date. For configured paths inside Git worktrees, replace those fallback dates with history-derived dates:

```bash
# all configured paths
./mm path scan --timestamp=git

# one configured path (ID or directory)
./mm path scan 1 --timestamp=git
./mm path scan ~/notes --timestamp=git
```

The default `--timestamp=filesystem` behavior remains unchanged. With `git`, the scan uses each file's first and latest Git author dates and follows renames. Non-Git paths and files with no Git history are left unchanged and reported. Re-run the scan after committing new document changes. Use `--json` for a machine-readable report.

### TUI date filters

In the TUI filter input, type a date token and press Space or Enter to turn it into a colored tag. Multiple tags use AND semantics:

```text
+2026-07                       modified during July 2026
+c:2025                        created during 2025
+m:2026-07..2026-09            modified from July through September
+m:7d                          modified in the last seven calendar days
```

Open ranges such as `+2026-07..` and `+..2026-07` are supported. Enter on ordinary text commits it as a purple filter tag; NAME and FULL tags retain the mode in which they were created, and all text and date tags use AND semantics. Enter on an empty input opens the selected document.

`/clear` removes all filter tags; Backspace on an empty input removes the most recently added tag. Esc hides the input without clearing active tags.

The TUI opens in modified-time order: the newest documents come first, and documents without a timestamp last. Outside the filter input, press `s` to toggle back to filename (dictionary) order; changing the sort focuses the new first row. In the tree view, Home and End jump to the first and last rows.

Press `p` to toggle the selected document's pinned state. Pinned documents stay visible in a marked, sticky section at the top regardless of scrolling, details height, or the active sort mode, and pins persist across TUI restarts.

Press `h` to toggle whether selection notes (`*-note.md`) appear in the list; the preference persists as the `hide notes` setting (also in the settings panel via `ctrl+o`). Press `?` to open the keyboard help overlay.

Press `d` to move the focused Markdown file to the trash after a `y/n` confirmation. This is a soft delete: the file moves into the path's hidden `.membox-trash/` directory and the document keeps its UUID, links, topics, and annotations but disappears from listings, search, graphs, and the Miru annotation DTO. Restore it with `mm trash restore <document-id>`; empty the trash with `mm trash purge` (by default only items older than 30 days, `--all` for everything).

### Command palette and agent input

The TUI reuses the bottom input field for distinct modes, identified by the colored badge. Open it with Space × 2, then use Ctrl+P to cycle modes:

- `NAME`: filename/ID filtering
- `FULL`: full-text search
- `CMD`: deterministic commands
- `AGENT`: natural-language prompts (agent backend is not connected yet)

In `CMD` mode, press Tab to progressively disclose commands and arguments. Choose a suggestion with ↑/↓ and Enter; Tab inserts it without executing. Supported commands mirror the CLI:

```text
note new <title>
topic create <name>
topic list
topic add <topic-id> <document-id>
topic remove <topic-id> <document-id>
topic documents <topic-id>
link add <from-id> <to-id>
link remove <from-id> <to-id>
link list <document-id>
link graph <document-id>
```

Use `/clear` in `NAME`/`FULL` mode to remove all filter tags.

In `CMD` mode, `link list <document-id>` opens a graph-focused board. The focused document appears first, followed by forward and backward linked cards; direction markers distinguish `→` outgoing from `←` incoming. Press `q` to leave the graph focus and return to the tree.

`link graph <document-id>` opens the one-hop canvas graph instead: backlinks fill the left column, the focused document sits in the center (with its topics below), and outgoing links fill the right column. Use `h`/`l` to hop between columns, `j`/`k` to move within one, and `enter` to open the selected card. On narrow terminals it falls back to the thread layout.

`@selected` refers to the currently selected document in argument positions.

## Document graph and topics

Relationships are stored only in SQLite; membox does not modify Markdown content to create links.

Create a note while reading another document:

```bash
./mm note new "Online Softmax Intuition" --from <document-id>
./mm note new "Online Softmax Intuition" --from <document-id> --no-open
```

The command creates the Markdown file, registers its document UUID, links it from the source document, and returns both the new document UUID and absolute path. It opens the note in the configured editor by default; use `--no-open` for automation.

Topics are ordinary root-level `topic-*.md` documents, so they can be searched, edited, pinned, Git-tracked, and used as human-readable index pages.

```bash
# document -> document manual links
./mm link add <from-id> <to-id>
./mm link remove <from-id> <to-id>
./mm link list <document-id>
./mm link graph <document-id>            # one-hop neighborhood (links + backlinks)
./mm link graph <document-id> --depth 2  # expand two hops
./mm link graph <document-id> --json     # nodes[] + edges[] for tooling

# document -> topic memberships
./mm topic create attention
./mm topic list
./mm topic add <topic-id> <document-id>
./mm topic remove <topic-id> <document-id>
./mm topic documents <topic-id>
```

A document can belong to multiple topics, and a topic can contain multiple documents. `link list` shows outgoing links, incoming links, and assigned topics. `link graph` walks the same edges as a neighborhood: `--depth N` expands N hops in both directions, and `--json` emits `{focus, nodes[], edges[]}` so external tools can render their own visualization.

## Web view

Render any indexed Markdown in the browser with the embedded [Miru](https://github.com/fivetiaowuu/miru) reader, served by the **Web Companion** — a single background process per membox home that owns the HTTP reader, the browser bridge, and web-originated saves. The default viewer is configurable and persists across sessions — set it with `ctrl+o` in the TUI (settings panel):

```bash
# open a note with the configured viewer
./mm note view <document-id>

# one-off overrides (do not change the configured viewer)
./mm note view <document-id> --web
./mm note view <document-id> --leaf
./mm note view <document-id> --web --no-open
```

The same applies in the TUI: pressing `enter` on a document opens it with the configured viewer. The current viewer is shown in the status bar.

### Web Companion lifecycle

One companion runs per membox home; the TUI, CLI, and browser extension all share it instead of starting their own servers. The TUI starts it automatically and shows a permanent badge in the status bar:

```text
WEB ● :8787 · 2 tabs · saved      running, all notes synced
WEB ● :8787 · 1 tab · 1 unsaved   a browser tab holds unsaved notes
WEB ◐ starting                     spawning
WEB ○ off                          stopped
WEB ! unavailable                  start failed (see ~/.membox/companion.log)
```

What happens to the companion when the TUI quits is the `web on exit` setting (`ctrl+o`, or `:web keep`): `ask` (default) shows a prompt when browser tabs are connected, `stop` shuts the companion down with the TUI, and `keep` leaves it running. Choosing `keep` in the prompt promotes a running companion so it survives.

```bash
./mm web status   # show companion state, tabs, unsaved counts
./mm web start    # start it detached (survives the terminal)
./mm web stop     # stop it
./mm web open     # open the reader in the browser
./mm serve        # run the companion in the foreground instead
```

Inside the TUI, `:web status / open / start / stop / keep` do the same, and `ctrl+o` adds `o` (open reader) and `x` (stop/start). Browser tabs heartbeat their unsaved state, so the badge and `mm web status` always reflect what is actually open.

The connection icon in the Miru top bar shows whether the reader is connected to membox; click it to toggle the connection. While connected, the lower-right Markdown arrow syncs both Markdown and annotations to membox (updating the current UUID, or creating a note when the paste is new). While disconnected, the same arrow keeps Miru's normal local download behavior. Every synced annotation is an individual `*-note.md` document: its blockquote/explanation is Markdown content, while SQLite stores the target UUID and anchoring hints. Miru assembles its annotation JSON only when the document is opened; it is not a second persisted note copy. Reading progress is separate DB UI state, so scrolling never rewrites note files. Notes and progress auto-save in the background for any document bound to membox; taking the first note on pasted (not yet synced) content automatically creates the membox source document while connected. The explicit arrow sync additionally writes the Markdown source.

### Settings panel

Press `ctrl+o` in the TUI to open the settings panel. Use ↑/↓ to select a setting and ←/→ (or space) to change its value — changes are saved immediately. The panel also shows the live Web Companion state (`o` opens the reader, `x` stops or starts it). Current settings:

- **viewer**: `leaf` or `web` — how `enter` opens documents
- **model**: `k3` or `grok-4.5` — the model used by the agent (reserved for the upcoming AI agent mode)
- **web on exit**: `ask`, `stop`, or `keep` — what happens to the Web Companion when the TUI quits

Settings persist in the membox database.

The companion listens on `127.0.0.1` only (preferring port `8787`, written to `~/.membox/bridge.json` for the browser extension). New synced notes are created under the first configured path.

## Development

Web code is split so Miru can be updated independently:

- `internal/web/frontend/miru/` — product-neutral Miru frontend
- `internal/web/frontend/membox/` — small membox browser adapter
- `internal/web/backend/` — Go HTTP/API implementation
- `internal/web/assets.go` — composition and embedding boundary

```bash
make check      # gofmt check, go vet, tests
make test       # unit and integration tests
make test-race  # race detector
make cover      # coverage summary
make smoke      # CLI smoke test
make build-all  # darwin/linux/windows artifacts
```

The equivalent low-level commands are:

```bash
go test ./...
go vet ./...
```

- [MVP development design](docs/development.md)
- [User stories and TDD acceptance scenarios](docs/user-stories.md)
