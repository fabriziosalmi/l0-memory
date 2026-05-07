# Contributing to l0-memory

Thanks for your interest. l0-memory is intentionally small — keep contributions in that spirit.

## Project layout

- `server/` — Go binary (`ltm`). Acts as both an MCP stdio server and a CLI for the same SQLite store.
- `extension/` — VSCode extension. UI to view, search, edit, and delete memories. Shells out to the `ltm` binary.

## Development

### Server

```sh
cd server
go build -o ltm .
./ltm list
./ltm save mykey "value" "tag1,tag2"
./ltm mcp   # MCP stdio mode (used by Claude Code, etc.)
```

The DB lives at `~/.long-term-memory/memories.db` by default. Override with `LTM_DB=/path/to/file.db`.

### Extension

```sh
cd extension
npm install
npm run compile
```

Open `extension/` in VSCode and press `F5` to launch the Extension Development Host.

## Pull requests

1. Open an issue first if the change is non-trivial — small scope wins.
2. Keep PRs focused: one logical change per PR.
3. Run the same checks CI runs:
   - `make test` — `go vet` + `go test -race ./...` for the server.
   - `cd extension && npm ci && npm run compile` — TypeScript compile.
   - `make vsix` — full extension build with bundled binaries (only required if
     you change `extension/` or the build script).
4. No new dependencies without a clear reason.

## Code style

- Go: standard `gofmt`. No frameworks; stdlib + `modernc.org/sqlite` only.
- TypeScript: `strict` mode (and the additional checks in `extension/tsconfig.json`).
  No bundler — `tsc` only.

## Reporting bugs

Use GitHub Issues. Include:
- OS, Go version, Node/VSCode version
- Exact steps to reproduce
- What you expected vs. what happened
