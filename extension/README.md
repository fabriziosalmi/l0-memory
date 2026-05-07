# l0-memory (VSCode extension)

Sidebar UI to view, search, edit, and delete entries in the
[long-term memory](https://github.com/fabriziosalmi/l0-memory) SQLite store.
The extension bundles platform-specific `ltm` binaries, so it works
out-of-the-box on macOS (arm64/x64), Linux (arm64/x64), and Windows (x64).

## Features

- TreeView of memories, sorted by most-recently-updated.
- **Add**, **edit**, **delete** entries from the view title bar / context menu.
- **Search** with a substring filter; **Clear search filter** button to reset.
- **Open in editor** to view a memory's full body in a Markdown tab.

## Settings

| Setting                  | Default | Description                                                                  |
|--------------------------|---------|------------------------------------------------------------------------------|
| `l0-memory.binaryPath`   | `""`    | Absolute path to `ltm`. Empty = use auto-discovery (bundled, then PATH).     |
| `l0-memory.dbPath`       | `""`    | Override SQLite DB path (sets `LTM_DB`). Empty = `~/.long-term-memory/…`.    |
| `l0-memory.autoStartMCP` | `false` | Spawn the MCP server in the background on activation. Usually unnecessary. |

## Build from source

```sh
cd extension
./scripts/build-bins.sh   # cross-compile ltm into bin/<goos>-<goarch>/
npm ci
npm run compile           # tsc → out/
npx @vscode/vsce package  # creates l0-memory-<version>.vsix
```

Or, from the repo root: `make vsix`.

## Local development

Press `F5` in VSCode with `extension/` open to launch an Extension Development
Host. The host picks up the dev binary at `../server/ltm` automatically.
