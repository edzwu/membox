# membox-doc — pi agent extension

Wraps the `mm doc` and `mm pdf` CLIs as a [pi](https://github.com/earendil-works/pi) coding-agent
extension: the agent can operate Markdown documents and import, search, inspect,
update, open, rename, trash, and restore PDFs. When toggled on, short IDs like
`11e8` in your messages are
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
| List URL resources | `/resources list` |
| Scan indexed inbox URL resources | `/resources scan` |

`/resources` is a thin Companion API client. URL extraction, canonicalization,
deduplication, provenance, knowledge-value classification, and review cursors
live in Membox's database; Timension does not duplicate those rows.

While ON, a message such as "把 membox 的 11e8 列入今天的任务" is augmented
with the resolved file context (`11e8 → membox-code-refactor.md`), so the
model knows exactly which file is meant.

## Deterministic video command

Use `/summarize` to bypass LLM tool selection and always run the complete video
pipeline directly:

```text
/summarize https://www.youtube.com/watch?v=...&list=...&index=13
```

It inspects the readable YouTube title and infers a course code when possible
(`CS 106L Fall 2019` → `cs106l-fall2019`). Otherwise it prompts for one. You can
also provide it explicitly:

```text
/summarize <youtube-url> cs106l-fall2019
/summarize <youtube-url> --course cs106l-fall2019
```

`/sumamrise` is an alias for the common misspelling. The command calls
`mm → mmd → echo-bp` directly, without relying on a model function-call decision.

## Tools

| Tool | Backing CLI | Notes |
|---|---|---|
| `membox_doc_resolve` | `mm doc show --json` | short ID / UUID / path → file |
| `membox_doc_search` | `mm doc search --json` | full-text over titles + bodies |
| `membox_doc_cat` | `mm doc cat` | read Markdown content (truncatable; use PDF tools for PDFs) |
| `membox_doc_list` | `mm doc list --json` | optional title/path filter |
| `membox_doc_create` | write file + `mm path scan` | any extension (not just .md) |
| `membox_doc_rename` | `mm doc rename` | keeps the UUID |
| `membox_doc_delete` | `mm doc delete` | soft delete (trash) + confirm |
| `membox_pdf_import` | `mm pdf import --json` | copy into managed PDF root + index; confirm |
| `membox_pdf_list` | `mm pdf list --json` | list PDF metadata |
| `membox_pdf_search` | `mm pdf search --json` | metadata + extracted-text search |
| `membox_pdf_show` | `mm pdf show --json` | identity, metadata, path, hash |
| `membox_pdf_update` | `mm pdf update --json` | update catalog metadata; confirm |
| `membox_pdf_open` | `mm pdf open` | system viewer (`open` on macOS) |
| `membox_pdf_rename` | `mm pdf rename --json` | preserve UUID; confirm |
| `membox_pdf_delete` | `mm pdf delete --json` | soft delete; confirm |
| `membox_pdf_restore` | `mm pdf restore --json` | restore original location; confirm |
| `membox_resource_ingest` | Companion `/api/resources/ingest` | deterministic URL capture into Membox |
| `membox_resource_list` | Companion `/api/resources` | knowledge-value ranking |
| `membox_resource_assess` | Companion `/api/resources/assess` | H/M/L + score + reason |
| `membox_resource_scan` | Companion `/api/resources/scan` | review cursor |
| `membox_question_ingest` | Companion `/api/questions/ingest` | deterministic open-question capture |
| `membox_question_list` | Companion `/api/questions` | personal question backlog |
| `membox_video_summarize` | `mm video summarize` → `mmd` → `echo-bp` | download one lecture, summarize it, and upsert `<course>-lec<N>.md` |

Document and PDF tools call the corresponding `mm doc` / `mm pdf` CLI commands;
resource tools use the Companion API. PDF binaries remain filesystem-authoritative
under the managed PDF root and are never read or written directly by the extension.

`membox_video_summarize` additionally requires `mmd` to be running for the same
home (`MEMBOX_HOME` or `--home`). It resolves echo-bp from `$MMD_EBP_BIN`, then
`~/repo/echo-bp/.venv/bin/ebp`, then `PATH`. Build both local binaries before use:

```bash
cd ~/repo/membox
go build -o bin/mm ./cmd/mm
go build -o bin/mmd ./cmd/mmd
bin/mmd run
```

The generated projection is flat and readable (`cs336-lec2.md`). Its lecture
number is inferred first from the human-facing video title (`Lecture 7`), not
from a possibly unrelated playlist slot (`index=13`); an explicit `lecture`
overrides inference, and playlist order is only a fallback. Both the selected
number/source and `playlist_index` are recorded in YAML front matter. Stable
YouTube course/video IDs ensure regeneration updates the same membox UUID.
