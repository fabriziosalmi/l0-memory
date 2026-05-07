# l0-memory (VSCode extension)

Sidebar UI for the [l0-memory](https://github.com/fabriziosalmi/l0-memory)
SQLite store: a Pinned pane plus a Memories pane with full CRUD,
scope-aware filtering, FTS5 search, knowledge-graph navigation, and a D3
force-directed visualisation of the graph. The extension bundles
platform-specific `ltm` binaries (darwin arm64/amd64, linux arm64/amd64,
windows amd64), so it runs out of the box from a `.vsix`.

## Features

- Two TreeViews:
  - **Pinned** — pinned memories first.
  - **Memories** — full list. Filterable by scope and FTS query. Optional
    grouping by scope. Sortable by `updated` (default), `created`, `key`,
    or `scope`. Multi-selectable for bulk delete.
- View title shows the active filters: `scope:user · q:"caddy" · sort:key
  · grouped`.
- Per-item context actions: Open in editor, Verify, Supersede with new
  key, Rename key, Edit, Link to, Show neighbors, Remove a link, Open
  graph from here, Pin / Unpin, Delete.
- Status bar item with the entry count and pinned count; click focuses
  the sidebar.
- Knowledge graph webview, D3 v7 bundled locally (no CDN). Coloured by
  scope class; pinned nodes outlined; click to open, double-click to
  re-root, drag to reposition, scroll to zoom; depth and direction wired
  to `memory_traverse`.

## Settings

| Setting                  | Default     | Description |
|--------------------------|-------------|-------------|
| `l0-memory.binaryPath`   | `""`        | Absolute path to `ltm`. Empty enables auto-discovery. |
| `l0-memory.dbPath`       | `""`        | Override SQLite DB path (sets `LTM_DB`). |
| `l0-memory.defaultScope` | `"user"`    | `user` / `ask` / `repo:current`. Used when adding from the sidebar. |
| `l0-memory.groupByScope` | `false`     | Group memories under collapsible scope nodes. |
| `l0-memory.sortBy`       | `"updated"` | `updated` / `created` / `key` / `scope`. Pinned items always come first. |
| `l0-memory.autoStartMCP` | `false`     | Spawn the MCP server on activation. Usually unnecessary because the host starts it. |

## Build from source

```sh
cd extension
./scripts/build-bins.sh   # cross-compile + ad-hoc codesign for darwin
npm ci
npm run compile           # tsc → out/
npx @vscode/vsce package  # produces l0-memory-<version>.vsix
```

Or, from the repository root: `make vsix`.

## Local development

Open `extension/` in VSCode and press `F5` to launch the Extension
Development Host. The host picks up the dev binary at `../server/ltm`
automatically.
