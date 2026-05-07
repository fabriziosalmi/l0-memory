# l0-memory developer Makefile.
# Convenience targets only — CI does the canonical builds.

VERSION ?= dev
LDFLAGS  := -s -w -X main.Version=$(VERSION)

.PHONY: help build test vet server-bin extension-bins extension-compile vsix clean install-mcp

help:
	@echo "Targets:"
	@echo "  build             — build server binary into server/ltm"
	@echo "  test              — go vet + go test (race) for server"
	@echo "  vet               — go vet ./... in server/"
	@echo "  extension-bins    — cross-compile ltm into extension/bin/<os>-<arch>/"
	@echo "  extension-compile — tsc compile of the extension"
	@echo "  vsix              — package the extension (.vsix)"
	@echo "  install-mcp       — register the local ltm with claude code"
	@echo "  install-mcp-desktop — register the local ltm with Claude Desktop"
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

install-mcp: build
	@echo "Registering ltm MCP server with Claude Code…"
	@command -v claude >/dev/null 2>&1 || { echo "claude CLI not found in PATH" >&2; exit 1; }
	claude mcp add l0-memory $(CURDIR)/server/ltm mcp

install-mcp-desktop: build
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
	jq --arg bin "$(CURDIR)/server/ltm" \
	  '.mcpServers["l0-memory"] = {command: $$bin, args: ["mcp"]}' \
	  "$$cfg" > "$$cfg.tmp" && mv "$$cfg.tmp" "$$cfg"; \
	echo "wrote $$cfg"; \
	echo "→ restart Claude Desktop (Cmd+Q then reopen) to pick up the change."

clean:
	rm -f server/ltm server/ltm.exe
	rm -rf extension/bin
	rm -f extension/*.vsix
