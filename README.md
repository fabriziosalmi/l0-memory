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

## Use with Claude Code

Register the local binary as an MCP server:

```sh
make install-mcp
# equivalent to:
# claude mcp add l0-memory $(pwd)/server/ltm mcp
```

Or edit `~/.claude.json` manually:

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
| `memory_save`   | `key`, `value`, `tags?`   | Insert or update an entry by key        |
| `memory_get`    | `key`, `expand?`          | Compact descriptor by default; pass `expand:true` for the full record |
| `memory_search` | `query`, `limit?`, `expand?` | FTS5 search; compact hits with snippet+score by default, full records on `expand:true` |
| `memory_list`   | `limit?`                  | Most recently updated entries           |
| `memory_delete` | `key`                     | Remove an entry                         |
| `memory_query`  | `key`, `path`             | Slice a JSON-valued memory by JSON Pointer (+ `*` wildcard) |

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
ltm list [limit]
ltm get <key>
ltm search <query> [limit]
ltm query <key> [path]             # JSON Pointer + '*' wildcard slice of a JSON memory
ltm save <key> <value|-> [tags]    # value of "-" reads from stdin
ltm delete <key>
ltm path                           # prints the SQLite DB path
ltm version                        # prints the build version
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

The extension auto-discovers the `ltm` binary in this order:

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

### Settings

| Setting                  | Default | Description                                                                    |
|--------------------------|---------|--------------------------------------------------------------------------------|
| `l0-memory.binaryPath`   | `""`    | Absolute path to `ltm`. Empty = use the discovery rules above.                 |
| `l0-memory.dbPath`       | `""`    | Override the SQLite DB path (sets `LTM_DB`). Empty = `~/.long-term-memory/`.   |
| `l0-memory.autoStartMCP` | `false` | Spawn the MCP stdio server in the background on activation. Usually unneeded. |

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
