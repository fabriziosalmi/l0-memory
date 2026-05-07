# l0-memory

[![ci](https://github.com/fabriziosalmi/l0-memory/actions/workflows/ci.yml/badge.svg)](https://github.com/fabriziosalmi/l0-memory/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/fabriziosalmi/l0-memory?sort=semver)](https://github.com/fabriziosalmi/l0-memory/releases)
[![go.mod](https://img.shields.io/github/go-mod/go-version/fabriziosalmi/l0-memory?filename=server%2Fgo.mod)](server/go.mod)
[![license](https://img.shields.io/github/license/fabriziosalmi/l0-memory)](LICENSE)

Minimal long-term memory for AI assistants. A single Go binary speaks the
[Model Context Protocol](https://modelcontextprotocol.io/) over stdio and
exposes the same SQLite store via a small CLI. A companion VSCode extension
provides a sidebar UI to view, search, edit, and delete entries.

- **No Python**, no embeddings, no vector DB — just SQLite.
- **One binary, two modes**: `ltm mcp` (stdio MCP server) and
  `ltm <list|get|search|save|delete>` (CLI).
- **Pure Go SQLite** (`modernc.org/sqlite`) — no CGO, builds anywhere.
- **VSCode extension bundles binaries** for darwin/linux/windows so it works
  out-of-the-box from a `.vsix`.

## Repository layout

```
server/      Go MCP server + CLI (the `ltm` binary)
extension/   VSCode extension (TreeView UI + bundled binaries)
```

## Install

### Pre-built (recommended)

Grab the latest release from the [releases page](https://github.com/fabriziosalmi/l0-memory/releases):

- `ltm-<os>-<arch>.tar.gz` (or `.zip` on Windows) — extract and place `ltm` on
  your `PATH`.
- `l0-memory-<version>.vsix` — install with
  `code --install-extension l0-memory-<version>.vsix`.

### From source

```sh
make build           # builds server/ltm
make test            # go vet + go test -race
make vsix            # cross-compiles all platform binaries and packages the .vsix
```

The DB lives at `~/.long-term-memory/memories.db`. Override with `LTM_DB=/path/to.db`.

## Use it with your AI tools

The server is a stdio MCP process — every host that speaks MCP can use the
*same* SQLite store, so memories you save from one tool are visible from the
other. Scope memories with `scope:"user"` (default) for cross-project notes,
or `scope:"repo:..."` / `scope:"desktop"` to partition.

### Claude Code

```sh
make install-mcp
# equivalent to:
# claude mcp add l0-memory $(pwd)/server/ltm mcp
```

### Claude Desktop

```sh
make install-mcp-desktop
# edits ~/Library/Application Support/Claude/claude_desktop_config.json
# (Linux: ~/.config/Claude/claude_desktop_config.json), backups the
# previous file, and points Claude Desktop at the local ltm binary.
# Restart Claude Desktop (Cmd+Q + reopen) to pick up the change.
```

### Cursor / Cline / any other MCP host

Use the same idea: point the host at `ltm` with args `["mcp"]`. Example
config snippet (host-agnostic):

```json
{
  "mcpServers": {
    "l0-memory": {
      "command": "/absolute/path/to/ltm",
      "args": ["mcp"]
    }
  }
}
```

### Available MCP tools

| Tool            | Args                      | What it does                            |
|-----------------|---------------------------|-----------------------------------------|
| `memory_save`     | `scope?`, `key`, `value`, `tags?`           | Insert or update an entry by `(scope, key)` |
| `memory_get`      | `scope?`, `key`, `expand?`                  | Compact descriptor by default; `expand:true` for the full record |
| `memory_search`   | `scope?`, `query`, `limit?`, `expand?`      | FTS5 search; compact hits with snippet+score by default |
| `memory_list`     | `scope?`, `limit?`                          | Most recently updated entries (pinned first) |
| `memory_delete`   | `scope?`, `key`                             | Remove an entry |
| `memory_query`    | `scope?`, `key`, `path`                     | Slice a JSON-valued memory by JSON Pointer (+ `*` wildcard) |
| `memory_pin`      | `scope?`, `key`, `pinned`                   | Toggle the pinned flag |
| `memory_link`     | `from_scope?`, `from_key`, `to_scope?`, `to_key`, `rel` | Create a typed edge between two memories |
| `memory_unlink`   | `from_scope?`, `from_key`, `to_scope?`, `to_key`, `rel` | Remove a typed edge |
| `memory_links`    | `scope?`, `key`                             | List every link incident to a memory |
| `memory_traverse` | `scope?`, `key`, `depth?`, `rel?`, `direction?` | BFS subgraph view |
| `memory_rename`   | `scope?`, `old_key`, `new_key`              | Rename a key atomically; cascades through links |

Pinned memories are also exposed as **MCP resources** at
`memory:///<scope>/<key>` so an MCP host can attach them to context with no
explicit `memory_get`. The server emits `notifications/resources/list_changed`
on pin/unpin/delete.

### Search semantics

`memory_search` is backed by SQLite FTS5 with the `unicode61` tokenizer:

- Each whitespace-separated token becomes a **prefix match**, AND'd with the
  others. Example: `caddy waf` matches entries containing both `caddy*` and
  `waf*` in any order, ranked by BM25.
- Tokens with special characters are quoted as **phrases**, so queries like
  `100%` or `"hello` don't break the parser.
- Punctuation in the data (`_`, `-`, `.`, `:`, `/`, …) is treated as a
  separator at index time, so `repo:caddy-waf` is matched by `caddy`, `waf`,
  or `repo`.
- Results are ordered by FTS5 rank, with `updated_at DESC` as the tiebreaker.
- If a query fails to parse as FTS5, `Search` transparently falls back to a
  case-insensitive `LIKE` substring scan with `%`/`_` treated literally.

## CLI reference

```sh
ltm [--scope <name>] <command> [args...]
# or set LTM_SCOPE in the environment for the same effect.

ltm list [limit]                              # pinned-first, by updated_at desc
ltm pinned [limit]                            # only pinned entries
ltm get <key>
ltm search <query> [limit]
ltm query <key> [path]                        # JSON Pointer + '*' wildcard slice
ltm save <key> <value|-> [tags]               # value of "-" reads from stdin
ltm delete <key>
ltm rename <old_key> <new_key>                # cascades through links
ltm pin <key>                                 # toggle pin on
ltm unpin <key>                               # toggle pin off
ltm link <from_key> <rel> <to_key>            # same-scope edge
ltm unlink <from_key> <rel> <to_key>
ltm links <key>                               # all incident edges
ltm traverse <key> [depth]                    # BFS graph view (JSON)
ltm path                                      # prints the SQLite DB path
ltm version                                   # prints the build version
```

### `memory_search` returns hits with snippet + score

Over MCP, `memory_search` returns an array of compact hits — `{key, tags,
score, snippet, size_bytes, created_at, updated_at}` — *not* the full Memory
records. The `snippet` is produced by FTS5 and surrounds the match with
`<<...>>` markers, e.g. `"...members\":[\"<<caddy>>-waf\",..."`. `score` is
`-bm25()` so larger = more relevant.

Because the snippet usually shows you what you needed, **you almost never
need a follow-up `memory_get`** after a search. When you do, pass
`expand:true` to `memory_search` (or call `memory_get` separately). The CLI
`ltm search` still returns the full Memory records — scripts keep working.

### Scopes (cross-tool, cross-project)

A memory is identified by `(scope, key)` — `scope` defaults to `"user"`.
The same `key` can live independently in multiple scopes, so
`scope:"user", key:"focus"` and `scope:"repo:l0-memory", key:"focus"` are
different rows. `memory_search` and `memory_list` accept `scope` to
restrict to one namespace; omit it to query every scope at once. Common
patterns:

- `user` — cross-project notes you want everywhere.
- `repo:<name>` — repo-specific context.
- `desktop` / `code` — host-specific notes when sharing the store across
  Claude Code, Claude Desktop, Cursor, etc.

### Pinning + auto-loaded context

Pin memories you want the host to surface automatically:

```sh
ltm pin focus_areas                   # in scope user
ltm --scope repo:l0-memory pin notes  # repo-scoped pin
```

Pinned memories appear as **MCP resources** at `memory:///<scope>/<key>`,
which means an MCP-aware client (Claude Code, Claude Desktop) can attach
them to context with no `memory_get` round-trip. The server emits
`notifications/resources/list_changed` whenever the pin set changes.

### Knowledge graph

Express relationships between memories instead of stuffing them in a JSON
blob:

```sh
ltm save tech:caddy "Caddy server" "tech"
ltm save repo:caddy-waf "WAF plugin" "repo"
ltm link repo:caddy-waf depends_on tech:caddy

ltm traverse tech:caddy 2
# {root: "memory:///user/tech:caddy", depth: 2, nodes: [...], edges: [...]}
```

`(from, to, rel)` triples are unique. Links cascade: deleting a memory
drops every edge incident to it. Edges are cross-scope, so a repo-scoped
memory can point at a user-scoped tech tag.

### `memory_get` is compact by default

Over MCP, `memory_get` returns a small descriptor — `key`, `tags`, `size_bytes`,
either a JSON `schema` (top-level keys + counts) or a UTF-8-safe `preview`,
and a `hint` field — but **not** the `value`. This keeps token usage low for
"is this entry there?" / "what shape is it?" probes. Pass `expand:true` to
get the full record. The CLI `ltm get` is unchanged: it always returns the
full record (so existing scripts keep working).

### Storing graphs without paying for them on every read

When a memory's value is JSON, prefer `memory_query` over `memory_get`:

```sh
ltm save user:github_graph - 'profile,user,graph' < graph.json   # 19 KB blob

ltm query user:github_graph /stats                # 50 bytes back
ltm query user:github_graph /clusters/web-security-caddy/members
ltm query user:github_graph '/repos/*/n'          # all names, 1 round-trip
```

This is the recommended pattern for graph-shaped or index-shaped memories:
keep a single canonical blob, slice it server-side. Sharding into one entry
per node is still possible but rarely needed once you can `memory_query` the
blob.

## VSCode extension

The sidebar has two panes:
- **Pinned** — pinned memories surface here first. Pin/unpin context actions.
- **Memories** — full list, filterable by scope (toolbar button) and by FTS
  search (toolbar button). Right-click any item for `Open in editor`,
  `Link to…`, `Show neighbors`, `Remove a link…`, `Open graph from here`,
  `Edit`, `Delete`, `Pin`/`Unpin`.

A **status bar item** on the right (`$(database) l0: N (📌 K)`) shows totals
and clicks back to the sidebar.

### Knowledge graph viewer

The `Open knowledge graph` button (toolbar of the Memories pane, or per-item
context action) launches a side webview that renders the memory store as a
D3 force-directed graph:

- Nodes coloured by scope (`user`, `feedback`, `repo:*`, other).
- Pinned memories outlined; the root node has a thicker outline.
- Click a node → open the memory in the editor; double-click → re-root the
  view on that node; drag to reposition; scroll to zoom.
- Depth (1–4) and direction (`out`/`in`/`both`) controls in the webview
  toolbar; changes re-fetch via `memory_traverse`.

### Settings

| Setting                  | Default | Description                                                                            |
|--------------------------|---------|----------------------------------------------------------------------------------------|
| `l0-memory.binaryPath`   | `""`    | Absolute path to `ltm`. Empty = auto-discovery (bundled, dev layout, common, PATH).    |
| `l0-memory.dbPath`       | `""`    | Override SQLite DB path (sets `LTM_DB`). Empty = `~/.long-term-memory/memories.db`.    |
| `l0-memory.defaultScope` | `"user"`| `user` / `ask` / `repo:current`. Determines where new memories go when added from UI. |
| `l0-memory.groupByScope` | `false` | Group memories under collapsible scope nodes in the Memories tree.                    |
| `l0-memory.sortBy`       | `"updated"` | `updated` / `created` / `key` / `scope`. Pinned items always come first.        |
| `l0-memory.autoStartMCP` | `false` | Spawn the MCP server in the background on activation. Usually unnecessary.            |

### Auto-discovery

The extension finds the `ltm` binary in this order:

1. The path set in `l0-memory.binaryPath` (settings).
2. The bundled binary inside the extension
   (`bin/<goos>-<goarch>/ltm`).
3. The dev layout (`../server/ltm` next to the extension folder, when running
   from the repo).
4. Common system locations: `/usr/local/bin`, `/opt/homebrew/bin`,
   `~/.local/bin`, `~/go/bin`.
5. Whatever `ltm` resolves to on `PATH`.

If the binary is missing, the sidebar offers a one-click action to open the
relevant setting or the output channel.

## Development

```sh
make build        # server/ltm
make test         # go vet + go test -race
make vsix         # full extension build incl. cross-compiled binaries
make clean
```

CI runs the same checks on Linux/macOS/Windows × Go 1.22 and 1.23.

See [CONTRIBUTING.md](CONTRIBUTING.md) for more.

## License

MIT — see [LICENSE](LICENSE).
