# membox

Local document identity and search for Markdown and PDF.

membox keeps managed files as the content authority and stores stable document UUIDs, current locations, metadata, and an FTS5 index in local SQLite. Markdown stays in its registered source directories; imported PDFs are copied to a dedicated filesystem directory rather than into SQLite.

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
./mm pdf import paper.pdf --title "Paper title" --authors "Author"
./mm pdf list
./mm pdf search "attention"
./mm pdf update <document-id> --keywords "gpu, kernels"
./mm pdf open <document-id>
./mm pdf server http://192.168.3.42:8000
./mm pdf convert <document-id>
./mm pdf rename <document-id> paper-v2.pdf
./mm pdf delete <document-id>
./mm pdf restore <document-id>
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

External edits are reflected after `mm path scan`. The scanner supports `.md`, `.markdown`, and `.pdf`. A conservative scanner preserves identity for unique filesystem-file-key and exact-hash renames; ambiguous matches are never automatically merged.

## PDF library

`mm pdf import` copies a PDF into `~/Documents/membox-pdfs` by default. Override the destination with `--to`; the chosen directory is remembered and registered as a scan path. PDF bytes remain ordinary filesystem files—SQLite stores only identity, location, SHA-256, extracted metadata, and the rebuildable FTS projection.

Embedded title, author, keywords, publication year, page count, and up to 16 MiB of extracted plain text are indexed. `mm pdf update` changes searchable catalog metadata without rewriting the binary. Rescans preserve these catalog overrides. PDF open/view actions use the operating system opener (`open` on macOS), while delete/restore use the same soft trash and stable UUID model as Markdown.

### Optional PDF-to-Markdown converter

The converter integration is an isolated optional feature; it does not add a SQLite schema dependency or put HTTP concerns in the core catalog/application service. Configure a compatible LAN service (the FastAPI `POST /convert/stream` API used by `pdf-converter`) and convert an indexed PDF:

```bash
./mm pdf server http://192.168.3.42:8000
./mm pdf convert <document-id>
```

The client streams one multipart request to `POST /convert/stream` with `format=zip` and incrementally decodes its NDJSON heartbeat, progress, error, and result events. Chunk/page progress is projected into the CLI and TUI; the final base64 ZIP contains one Markdown file plus its `images/` assets and may come from the server's PDF SHA-256/options cache. The ZIP parser accepts only a root Markdown file and safe `images/<filename>` entries, with traversal, entry-count, response-size, and expanded-size limits. Markdown is published under the configured main path with readable, stable filenames such as `<PDF stem>--pdf-<PDF UUID>.md` and `<PDF stem>--pdf-<PDF UUID>-chapter-001.md`. Existing UUID-only generated files are renamed in place on reconversion, preserving their document UUIDs and links; after first publication the readable stem is frozen so later PDF renames do not churn backlinks. Images stay out of the Markdown/Git tree under `<PDF directory>/.membox-assets/<PDF UUID>/images/`; generated image destinations use the read-only `/api/pdf-assets/<PDF UUID>/images/<filename>` route so Miru can render them without exposing arbitrary local files.

Converted Markdown is parsed with a Markdown AST. Converter-produced HTML table blocks are normalized to safe GFM pipe tables before publishing; `rowspan` and `colspan` are flattened by repeating their cell values, while table examples inside code or nested quotes are left untouched. When the AST contains at least two chapter headings, the result is postprocessed into an index-only document plus chapter documents. ATX and Setext headings are supported, while headings inside fenced/indented code, block quotes, and other nested blocks are not treated as book boundaries. Chapter names such as `第 1 章` and `Chapter 1`, together with introductions, appendices, and afterwords, become stable documents. If a converter keeps numbered chapter labels only as plain text under top-level `Table of Contents`, repeated `CONTENTS`, or `目录` headings, a guarded TOC fallback pairs those entries with normalized top-level body headings (including Unicode compatibility forms). It supports Chinese numbers, Arabic/Roman numerals, and English words such as `One` through `Twenty`; repeated chapter-summary headings prefer the later body occurrence, while prefaces and epilogues remain separate documents. At least two trusted matches are still required before splitting. When deterministic splitting finds no chapters and the converted Markdown is larger than 40 KB, the workflow automatically asks the local model for chapter boundaries through the same mmd → Pi → qwen3:14b path — no manual step, in both `mm pdf convert` and the TUI Tab conversion; when mmd is not running it silently keeps the document whole. The LLM receives only a compact structural sketch (headings + sampled paragraph openings) and returns anchors that must quote real document lines; table-row/TOC anchors are rejected, every anchor is located by code, and at least two located anchors are required — otherwise the document stays whole. The PDF links to the index in SQLite, and every index/chapter pair gets explicit links in both directions in addition to clickable relative Markdown links. Re-running conversion updates the same stable files and preserves their UUIDs.

Feature configuration lives in `~/.membox/pdf-converter.json` (or the selected `MEMBOX_HOME`), independently of the catalog database; `MEMBOX_PDF_CONVERTER_URL` is a non-persistent fallback. LAN converter traffic uses a feature-local HTTP transport that deliberately bypasses global proxy environment variables.

In the TUI select a PDF and press `tab` to convert it; the status bar shows an indeterminate progress bar and elapsed time while the synchronous converter runs. A `◆` beside a PDF means its stable converted Markdown index is present. The command palette still exposes `pdf convert`, and configures the endpoint with `pdf server <url>`.

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

The TUI opens in modified-time order: the newest documents come first, and documents without a timestamp last. Markdown and PDF files share one tree. Press `ctrl+p` to cycle its media scope through all documents → Markdown → PDF → images; a full-word status badge is shown whenever the scope is not all, and the PDF-only tree includes a right-aligned file-size column. The image scope is ready for image documents once image ingest lands. `ctrl+r` reloads the catalog immediately before running a full path scan, then focuses the newest visible document added since the previous tree snapshot—so a PDF imported by Miru appears without waiting for the scan. Outside the filter input, `s` summarizes the selected document. In the tree view, Home and End jump to the first and last rows.

Press `p` to toggle the selected document's pinned state. Pinned documents stay visible in a marked, sticky section at the top regardless of scrolling or details height, and pins persist across TUI restarts.

Press `ctrl+h` to toggle whether selection notes (`*-note.md`) appear in the list; the preference persists as the `hide notes` setting (also in the settings panel via `ctrl+o`). Press `?` to open the keyboard help overlay.

Press `d` to move the focused document file to the trash after a `y/n` confirmation. This is a soft delete: the file moves into the path's hidden `.membox-trash/` directory and the document keeps its UUID, links, topics, and annotations but disappears from listings, search, graphs, and the Miru annotation DTO. Restore it with `mm trash restore <document-id>`; empty the trash with `mm trash purge` (by default only items older than 30 days, `--all` for everything).

### Command palette and agent input

The TUI reuses the bottom input field for distinct modes, identified by the colored badge:

- Space × 2 opens `NAME` filename/ID filtering; `ctrl+f` toggles name/content search.
- `:` opens `CMD` deterministic commands.
- `ctrl+k` opens `AGENT` natural-language prompts.
- `ctrl+p` keeps its global meaning while the input is focused: cycle the tree's media scope.

In `CMD` mode, press Tab to progressively disclose commands and arguments. Choose a suggestion with ↑/↓ and Enter; Tab inserts it without executing. Supported commands mirror the CLI:

```text
doc new <title>
pdf server <url>
pdf convert [document-id]
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

Create a document while reading another one (`mm note` still works as a deprecated alias for these):

```bash
./mm doc new "Online Softmax Intuition" --from <document-id>
./mm doc new "Online Softmax Intuition" --from <document-id> --no-open
```

The command creates the Markdown file, registers its document UUID, links it from the source document, and returns both the new document UUID and absolute path. It opens the document in the configured editor by default; use `--no-open` for automation.

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
# open a document with the configured viewer
./mm doc view <document-id>

# one-off overrides (do not change the configured viewer)
./mm doc view <document-id> --web
./mm doc view <document-id> --leaf
./mm doc view <document-id> --web --no-open
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

What happens to the companion when the TUI quits is the `web on exit` setting (`ctrl+o`, or `:web keep`): `ask` (default) shows a prompt, `stop` releases this TUI's session lease, and `keep` promotes the companion before releasing the lease. A session companion stops when its last TUI lease is released or expires, so closing one of several TUIs never disrupts the others. Choosing `[s] Stop web and quit`, `:web stop`, or `mm web stop` is an explicit global stop.

```bash
./mm web status   # show companion state, tabs, unsaved counts
./mm web start    # start it detached (survives the terminal)
./mm web stop     # stop it
./mm web open     # open the reader in the browser
./mm serve        # run the companion in the foreground instead
```

Inside the TUI, `:web status / open / start / stop / keep` do the same, and `ctrl+o` adds `o` (open reader) and `x` (stop/start). Each TUI renews an independent controller lease. Browser tabs send same-origin POST heartbeats with their unsaved state, so the badge and `mm web status` reflect what is actually open. All TUI, CLI, and Companion data mutations also share `$MEMBOX_HOME/mutation.lock`, serializing each complete file-and-index update across processes.

The connection icon in the Miru top bar shows whether the reader is connected to membox; click it to toggle the connection. While connected, dropping one PDF onto Miru uploads it to the remembered managed PDF directory (by default `~/Documents/membox-pdfs`) and catalogs it without replacing the Markdown currently being read; uploads are limited to 500 MiB. The lower-right Markdown arrow syncs both Markdown and annotations to membox (updating the current UUID, or creating a note when the paste is new). While disconnected, the same arrow keeps Miru's normal local download behavior. Every synced annotation is an individual `*-note.md` document: its blockquote/explanation is Markdown content, while SQLite stores the target UUID and anchoring hints. Miru assembles its annotation JSON only when the document is opened; it is not a second persisted note copy. Reading progress is separate DB UI state, so scrolling never rewrites note files. Notes and progress auto-save in the background for any document bound to membox; taking the first note on pasted (not yet synced) content automatically creates the membox source document while connected. The explicit arrow sync additionally writes the Markdown source.

The `译` action in Miru's lower-left document controls toggles disposable bilingual immersive translation. Miru sends one rendered prose block at a time, starting near the current viewport; Web Companion forwards an NDJSON stream over mmd's owner-only Unix socket, and mmd runs an isolated tool-free `pi --mode rpc` turn with local `qwen3:14b`. A build-pinned Pi provider extension uses Ollama's native streaming API with `think:false` (the OpenAI-compatible endpoint ignores Qwen3's no-thinking controls), so translation starts without generating hundreds of hidden reasoning tokens. Pi text deltas appear immediately below the source block before the next block starts, so document length does not consume one shared model context. Completed translations are cached in a standalone, disposable database (`~/.membox/mmd/translation-cache.db`, outside the core catalog) keyed by normalized source text + target language + model + prompt version: reopening or re-translating a document replays cached paragraphs instantly without starting Pi. Toggling off or opening another document aborts the active request and removes translated DOM without changing Markdown or SQLite. This requires local Ollama with `qwen3:14b` pulled. mmd starts on demand — the first translation or structure-planning request spawns it automatically; a stale daemon is replaced transparently, and crashes lose nothing beyond the in-flight request (the cache is on disk).

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
