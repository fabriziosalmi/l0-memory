# Configuration Reference

**l0-memory** can be configured via environment variables and VS Code settings.

## Environment Variables

These variables affect the behavior of the `ltm` binary, whether run as a CLI or an MCP server.

| Variable | Description | Default |
| :--- | :--- | :--- |
| `LTM_DB` | Absolute path to the SQLite database file. | `~/.long-term-memory/memories.db` |
| `LTM_SCOPE` | Sets the default scope for all commands. | `user` |
| `LTM_DEBUG` | If set to `1`, enables verbose debug logging to stderr. | (empty) |
| `LTM_LOG_FILE` | Path to a file where debug logs should be appended. | (empty) |
| `LTM_EMBEDDING_URL` | OpenAI-compatible `/v1/embeddings` base URL. Enables [hybrid retrieval](/guide/features-hybrid). Empty = FTS-only. | (empty) |
| `LTM_EMBEDDING_MODEL` | Embedding model name passed to the endpoint. | (empty) |
| `LTM_EMBED_DISABLE` | Set to `1` to force the vector path off even when a URL is configured. | (empty) |
| `LTM_EMBED_TIMEOUT` | Per-request embedding timeout as a Go duration (e.g. `3s`). | `5s` |
| `LTM_CONFLICT_DISABLE` | Set to `1` to disable automatic local conflict resolution (Auto-Supersede) on save. | (empty) |

::: info Debugging
Setting `LTM_LOG_FILE` automatically enables `LTM_DEBUG`. This is useful for troubleshooting MCP connection issues where stderr might be swallowed by the host.
:::

## VS Code Settings

These settings are available in the VS Code Settings UI under the `l0-memory` section.

### `l0-memory.binaryPath`
**Type:** `string` | **Default:** `""`
The absolute path to the `ltm` binary. If left empty, the extension will attempt to auto-discover the binary in common locations and the extension's bundled `bin/` folder.

### `l0-memory.dbPath`
**Type:** `string` | **Default:** `""`
Overrides the SQLite database path. This effectively sets the `LTM_DB` environment variable for the extension's internal operations.

### `l0-memory.defaultScope`
**Type:** `enum` | **Default:** `"user"`
Options: `user`, `ask`, `repo:current`.
Determines the scope used when adding a new memory from the sidebar. `repo:current` will attempt to use the name of your active workspace folder.

### `l0-memory.groupByScope`
**Type:** `boolean` | **Default:** `false`
When enabled, the Memories tree view will group entries under collapsible scope nodes.

### `l0-memory.sortBy`
**Type:** `enum` | **Default:** `"updated"`
Options: `updated`, `created`, `key`, `scope`.
Sets the default sort order for the Memories tree view. Note that pinned items always appear at the top regardless of this setting.

### `l0-memory.autoStartMCP`
**Type:** `boolean` | **Default:** `false`
If true, the extension will spawn a background MCP server on activation. This is generally unnecessary if you are using an MCP host like Claude Code that starts its own instance.
