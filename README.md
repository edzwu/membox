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

Outside the filter input, press `s` to toggle between filename order and modified-time order. Time order shows the newest documents first and places documents without a timestamp last; changing the sort focuses the new first row. In the tree view, Home and End jump to the first and last rows.

## Development

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
