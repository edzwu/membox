.PHONY: build test clean install check-tui kill-tui run run-mmd kill-mmd check-mmd

BIN_DIR := bin
MM_BIN := $(BIN_DIR)/mm
MMD_BIN := $(BIN_DIR)/mmd

build: $(MM_BIN) $(MMD_BIN)

$(MM_BIN):
	@mkdir -p $(BIN_DIR)
	go build -o $(MM_BIN) ./cmd/mm

$(MMD_BIN):
	@mkdir -p $(BIN_DIR)
	go build -o $(MMD_BIN) ./cmd/mmd

test:
	go test ./...

# Development helper: keep all config/data under ./.membox
run: kill-tui
	MM_DEV=1 go run ./cmd/mm

run-tui: run

# Development helper: run the daemon in dev mode, killing any previous one.
run-mmd: kill-mmd
	MM_DEV=1 go run ./cmd/mmd start

# Built-binary variant of run-mmd.
run-mmd-built: build kill-mmd
	MM_DEV=1 ./$(MMD_BIN) start

# Test daemon connectivity in dev mode.
check-mmd:
	MM_DEV=1 go run ./cmd/mmd check

# Convenience target to forcibly terminate any orphaned mmd processes.
kill-mmd:
	@PIDS=$$(ps aux | grep -E 'mmd$$|mmd ' | grep -v grep | awk '{print $$2}' | tr '\n' ' '); \
	if [ -n "$${PIDS% }" ]; then \
		kill $${PIDS% }; \
		echo "killed mmd process(s): $${PIDS% }"; \
	else \
		echo "no running mmd process found"; \
	fi

# Development helper: forcibly terminate any orphaned mm processes.
kill-tui:
	@PIDS=$$(ps aux | grep -E 'mm$$|mm ' | grep -v grep | awk '{print $$2}' | tr '\n' ' '); \
	if [ -n "$${PIDS% }" ]; then \
		kill $${PIDS% }; \
		echo "killed mm process(s): $${PIDS% }"; \
	else \
		echo "no running mm process found"; \
	fi

clean:
	rm -rf $(BIN_DIR) .membox

install: build
	install -d $(DESTDIR)/usr/local/bin
	install -m 0755 $(MM_BIN) $(DESTDIR)/usr/local/bin/mm
	install -m 0755 $(MMD_BIN) $(DESTDIR)/usr/local/bin/mmd
