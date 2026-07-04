# l0-memory developer Makefile.
# Convenience targets only — CI does the canonical builds.

VERSION    ?= 0.9.0
LDFLAGS    := -s -w -X main.Version=$(VERSION)
INSTALL_BIN := $(HOME)/.local/bin/ltm

.PHONY: help build test vet server-bin extension-bins extension-compile vsix clean install install-mcp install-mcp-desktop install-claude

help:
	@echo "Targets:"
	@echo "  build             — build server binary into server/ltm"
	@echo "  test              — go vet + go test (race) for server"
	@echo "  vet               — go vet ./... in server/"
	@echo "  extension-bins    — cross-compile ltm into extension/bin/<os>-<arch>/"
	@echo "  extension-compile — tsc compile of the extension"
	@echo "  vsix              — package the extension (.vsix)"
	@echo "  install           — build + copy binary to $(INSTALL_BIN) + codesign"
	@echo "  install-mcp       — register the installed ltm with Claude Code (depends on install)"
	@echo "  install-mcp-desktop — register the installed ltm with Claude Desktop (depends on install)"
	@echo "  install-claude    — install the Claude Code auto-recall hook + /checkpoint skill"
	@echo "  clean             — remove server binary, extension/bin, .vsix files"

build:
	cd server && go build -trimpath -ldflags="$(LDFLAGS)" -o ltm .
	@# macOS Sequoia/Tahoe will silently kill an unsigned subprocess spawned
	@# by a signed app (Claude Desktop, etc). An ad-hoc signature is enough
	@# to flip it to "trusted" without enrolling in the Apple developer
	@# program. See SECURITY.md "macOS provenance gate".
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		codesign --sign - --force --timestamp=none server/ltm 2>&1 | grep -v "replacing existing" || true; \
	fi

vet:
	cd server && go vet ./...

test:
	cd server && go vet ./... && go test -count=1 -race -timeout 90s ./...

extension-bins:
	cd extension && ./scripts/build-bins.sh

extension-compile:
	cd extension && npm ci && npm run compile

vsix: extension-bins extension-compile
	cd extension && rm -f *.vsix && npx --yes @vscode/vsce package

install: build
	@# Iterative re-deploy target. Copies server/ltm over the user's
	@# installed binary path and re-applies the macOS provenance signature.
	@# The MCP host (Claude Desktop / Claude Code) keeps the OLD binary
	@# mapped in memory until you Quit + relaunch, so this is the FILE
	@# half of the deploy — the user has to do the RESTART half.
	@mkdir -p $(dir $(INSTALL_BIN))
	install -m 0755 server/ltm $(INSTALL_BIN)
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		codesign --sign - --force --timestamp=none $(INSTALL_BIN) 2>&1 | grep -v "replacing existing" || true; \
	fi
	@echo "→ ltm $(VERSION) installed at $(INSTALL_BIN)"
	@echo "→ Quit (Cmd+Q) and relaunch Claude Desktop / Claude Code to pick up the new binary."
	@echo "→ For hybrid retrieval, ensure LTM_EMBEDDING_URL and LTM_EMBEDDING_MODEL are set in the MCP host env."

install-mcp: install
	@echo "Registering ltm MCP server with Claude Code…"
	@command -v claude >/dev/null 2>&1 || { echo "claude CLI not found in PATH" >&2; exit 1; }
	claude mcp add l0-memory $(INSTALL_BIN) mcp

install-claude:
	@# Install the Claude Code auto-recall hook + /checkpoint skill into
	@# ~/.claude (see integrations/claude-code/). Idempotent.
	integrations/claude-code/install.sh

install-mcp-desktop: install
	@# Merge into existing l0-memory block instead of overwriting it. This
	@# preserves operator-set keys like `env` (LTM_EMBEDDING_URL, etc.)
	@# that downstream targets / hand edits care about.
	@echo "Registering ltm MCP server with Claude Desktop…"
	@command -v jq >/dev/null 2>&1 || { echo "jq not found (brew install jq)" >&2; exit 1; }
	@case "$$(uname -s)" in \
		Darwin) cfg="$$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;; \
		Linux)  cfg="$$HOME/.config/Claude/claude_desktop_config.json" ;; \
		*)      echo "Unsupported OS for this target" >&2; exit 1 ;; \
	esac; \
	mkdir -p "$$(dirname "$$cfg")"; \
	[ -f "$$cfg" ] || echo '{"mcpServers":{}}' > "$$cfg"; \
	cp "$$cfg" "$$cfg.bak.$$(date +%Y%m%d-%H%M%S)"; \
	jq --arg bin "$(INSTALL_BIN)" \
	  '.mcpServers["l0-memory"] |= ((. // {}) + {command: $$bin, args: ["mcp"]})' \
	  "$$cfg" > "$$cfg.tmp" && mv "$$cfg.tmp" "$$cfg"; \
	echo "wrote $$cfg (merged; existing env preserved)"; \
	echo "→ restart Claude Desktop (Cmd+Q then reopen) to pick up the change."

clean:
	rm -f server/ltm server/ltm.exe
	rm -rf extension/bin
	rm -f extension/*.vsix
