.PHONY: build test clean install install-completions check-tui kill-tui

BIN_DIR := bin
MM_BIN := $(BIN_DIR)/mm
MMD_BIN := $(BIN_DIR)/mmd

# Shell completion directories
ZSH_COMPLETIONS_DIR := $(HOME)/.config/zsh/completions

build: $(MM_BIN) $(MMD_BIN)

$(MM_BIN):
	@mkdir -p $(BIN_DIR)
	go build -o $(MM_BIN) ./cmd/mm

$(MMD_BIN):
	@mkdir -p $(BIN_DIR)
	go build -o $(MMD_BIN) ./cmd/mmd

test:
	go test ./...

# Convenience target to forcibly terminate any orphaned mm processes.
kill-tui:
	@PIDS=$$(ps aux | grep -E 'mm$$|mm ' | grep -v grep | awk '{print $$2}' | tr '\n' ' '); \
	if [ -n "$${PIDS% }" ]; then \
		kill $${PIDS% }; \
		echo "killed mm process(s): $${PIDS% }"; \
	else \
		echo "no running mm process found"; \
	fi

# Development helper: keep all config/data under ./.membox
run: kill-tui
	MM_DEV=1 go run ./cmd/mm

clean:
	rm -rf $(BIN_DIR) .membox

install: build
	install -d $(DESTDIR)/usr/local/bin
	install -m 0755 $(MM_BIN) $(DESTDIR)/usr/local/bin/mm
	install -m 0755 $(MMD_BIN) $(DESTDIR)/usr/local/bin/mmd

install-completions: build
	@mkdir -p $(ZSH_COMPLETIONS_DIR)
	@$(MM_BIN) completion zsh > $(ZSH_COMPLETIONS_DIR)/_mm
	@echo "Installed zsh completions to $(ZSH_COMPLETIONS_DIR)/_mm"
	@grep -q "fpath+=($(ZSH_COMPLETIONS_DIR))" $(HOME)/.zshrc || echo "fpath+=($(ZSH_COMPLETIONS_DIR))" >> $(HOME)/.zshrc
	@grep -q "autoload -Uz compinit && compinit" $(HOME)/.zshrc || echo "autoload -Uz compinit && compinit" >> $(HOME)/.zshrc
	@echo "Updated $(HOME)/.zshrc"
