.PHONY: all help build install clean fmt fmt-check vet lint test test-race cover cover-html check smoke run build-all clean-all

GO ?= go
BIN_DIR ?= bin
BINARY ?= $(BIN_DIR)/mm
MAIN ?= ./cmd/mm
PKG := ./...
COVERAGE ?= coverage.out

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS ?= -s -w -X membox.Version=$(VERSION)+$(COMMIT)

all: check build

help:
	@printf '%s\n' 'membox Go workflow'
	@printf '\n'
	@printf '%-18s %s\n' 'make build' 'build $(BINARY)'
	@printf '%-18s %s\n' 'make install' 'install mm to GOPATH/bin'
	@printf '%-18s %s\n' 'make run' 'build and run the TUI'
	@printf '%-18s %s\n' 'make test' 'run unit and integration tests'
	@printf '%-18s %s\n' 'make test-race' 'run tests with the race detector'
	@printf '%-18s %s\n' 'make cover' 'run tests and write $(COVERAGE)'
	@printf '%-18s %s\n' 'make cover-html' 'open HTML coverage report'
	@printf '%-18s %s\n' 'make fmt' 'format Go source'
	@printf '%-18s %s\n' 'make vet' 'run go vet'
	@printf '%-18s %s\n' 'make check' 'format check, vet, and tests'
	@printf '%-18s %s\n' 'make smoke' 'run a small CLI smoke test'
	@printf '%-18s %s\n' 'make build-all' 'build linux/darwin/windows artifacts'
	@printf '%-18s %s\n' 'make clean' 'remove build artifacts'

build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(MAIN)

install:
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags '$(LDFLAGS)' $(MAIN)

run: build
	$(BINARY)

clean:
	rm -rf $(BIN_DIR) $(COVERAGE) coverage.html

fmt:
	$(GO) fmt $(PKG)

fmt-check:
	@test -z "$$(gofmt -l cmd internal *.go | tee /dev/stderr)"

vet:
	$(GO) vet $(PKG)

lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		printf '%s\n' 'golangci-lint not installed; skipped'; \
	fi

test:
	$(GO) test $(PKG)

test-race:
	CGO_ENABLED=1 $(GO) test -race $(PKG)

cover:
	$(GO) test $(PKG) -coverprofile=$(COVERAGE)
	$(GO) tool cover -func=$(COVERAGE)

cover-html: cover
	$(GO) tool cover -html=$(COVERAGE) -o coverage.html
	open coverage.html

check: fmt-check vet test

smoke: build
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	mkdir -p "$$tmp/notes"; \
	printf '# Smoke\nneedle\n' >"$$tmp/notes/smoke.md"; \
	$(BINARY) --home "$$tmp/home" path add "$$tmp/notes" >/dev/null; \
	$(BINARY) --home "$$tmp/home" doc search needle --json | grep -q '"document_id"'; \
	$(BINARY) --home "$$tmp/home" doc list | grep -q 'Smoke'; \
	$(BINARY) --home "$$tmp/home" index status | grep -q 'Active:     1'; \
	printf '%s\n' 'smoke test passed'

build-all:
	@mkdir -p dist
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/mm-darwin-amd64 $(MAIN)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/mm-darwin-arm64 $(MAIN)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/mm-linux-amd64 $(MAIN)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/mm-linux-arm64 $(MAIN)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/mm-windows-amd64.exe $(MAIN)

clean-all: clean
	rm -rf dist
