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
