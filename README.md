# l0-memory

[![ci](https://github.com/fabriziosalmi/l0-memory/actions/workflows/ci.yml/badge.svg)](https://github.com/fabriziosalmi/l0-memory/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/fabriziosalmi/l0-memory?sort=semver)](https://github.com/fabriziosalmi/l0-memory/releases)
[![go.mod](https://img.shields.io/github/go-mod/go-version/fabriziosalmi/l0-memory?filename=server%2Fgo.mod)](server/go.mod)
[![license](https://img.shields.io/github/license/fabriziosalmi/l0-memory)](LICENSE)

Long-term memory for AI assistants, backed by a single Go binary that speaks
the [Model Context Protocol](https://modelcontextprotocol.io/) over stdio
and exposes the same SQLite store via a CLI. Memories are partitioned by
scope, can be pinned, linked into a typed graph, and tracked for freshness.
A VSCode extension provides a sidebar UI, including a force-directed
visualisation of the graph.

The store is local and plaintext. By default there is no network listener
and no embeddings — search is pure SQLite FTS5. Point `LTM_EMBEDDING_URL`
at an OpenAI-compatible endpoint (Ollama, LM Studio, vLLM, …) to opt into
hybrid retrieval, which blends FTS5 with vector similarity. The Go side has
zero CGO dependencies; the binary cross-compiles to every supported
platform.

## Repository layout

```
server/      Go MCP server + CLI (the `ltm` binary)
extension/   VSCode extension (TreeView UI + bundled binaries)
```

## Install

### From a release

Download from the [releases page](https://github.com/fabriziosalmi/l0-memory/releases):

- `ltm-<os>-<arch>.tar.gz` (or `.zip` on Windows). Extract and place `ltm` on
  your `PATH`.
- `l0-memory-<version>.vsix`. Install the extension with
  `code --install-extension l0-memory-<version>.vsix`.

The macOS binaries in the release archives are ad-hoc codesigned. See
[SECURITY.md](SECURITY.md) for the rationale (Sequoia/Tahoe provenance gate).

### From source

```sh
make build           # server/ltm
make test            # go vet + go test -race
make vsix            # cross-compile all binaries + package the extension
```

The default DB path is `~/.long-term-memory/memories.db`. Override with
`LTM_DB=/path/to.db`.

## Connecting an MCP host

`ltm` is a stdio MCP process. Every host that speaks MCP can use the same
SQLite store, so a memory saved from one tool is visible from another.

### Claude Code

```sh
make install-mcp
# equivalent to: claude mcp add l0-memory $(pwd)/server/ltm mcp
```

### Claude Desktop

```sh
make install-mcp-desktop
# Edits the Claude Desktop config (macOS:
# ~/Library/Application Support/Claude/claude_desktop_config.json,
# Linux: ~/.config/Claude/claude_desktop_config.json), backs up the
# previous file, and points Claude Desktop at the local ltm binary.
# Quit and reopen Claude Desktop to pick up the change.
```

### Cursor / Cline / any other MCP host

Point the host at `ltm` with args `["mcp"]`. Example config snippet:

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

## MCP tools

| Tool               | Required args                       | Optional args                                | Behaviour |
|--------------------|-------------------------------------|----------------------------------------------|-----------|
| `memory_save`      | `key`, `value`                      | `scope`, `tags`, `origin`, `origin_agent`    | Insert or update by `(scope, key)`. `origin`/`origin_agent` record provenance. |
| `memory_get`       | `key`                               | `scope`, `expand`                            | Compact descriptor by default; pass `expand:true` for the full record. |
| `memory_search`    | `query`                             | `scope`, `limit`, `expand`                   | Search over key, value, tags. FTS5 by default; hybrid (FTS5 + vector) when an embedding endpoint is configured. Compact hits with snippet and score. |
| `memory_list`      | —                                   | `scope`, `limit`                             | Most recently updated entries, pinned first, archived hidden. |
| `memory_delete`    | `key`                               | `scope`                                      | Remove an entry. Cascades to incident links. |
| `memory_query`     | `key`, `path`                       | `scope`                                      | Slice a JSON-valued memory by JSON Pointer (RFC 6901) plus `*` wildcard. |
| `memory_pin`       | `key`, `pinned`                     | `scope`                                      | Toggle the pinned flag. Pinning implies a verify. |
| `memory_link`      | `from_key`, `to_key`, `rel`         | `from_scope`, `to_scope`                     | Create a typed edge `(from, to, rel)`. Re-linking is a no-op. |
| `memory_unlink`    | `from_key`, `to_key`, `rel`         | `from_scope`, `to_scope`                     | Remove a single edge. |
| `memory_links`     | `key`                               | `scope`                                      | List every link incident to a memory, in either direction. |
| `memory_traverse`  | `key`                               | `scope`, `depth`, `rel`, `direction`         | BFS subgraph view. Depth defaults to 1; direction `out`/`in`/`both`. |
| `memory_rename`    | `old_key`, `new_key`                | `scope`                                      | Atomic rename inside one scope. Cascades through incident links. |
| `memory_verify`    | `key`                               | `scope`                                      | Set `verified_at = now`. Compact views report `staleness_days`. |
| `memory_supersede` | `old_key`, `new_key`, `value`       | `scope`, `tags`                              | Archive the old key, create the new, link new `--supersedes→` old. |

Pinned memories are also exposed as MCP **resources** at
`memory:///<scope>/<key>`, so an MCP host that supports
`resources/list` + `resources/read` can attach pinned context without
calling `memory_get`. The server emits
`notifications/resources/list_changed` whenever the pinned set changes
(pin, unpin, supersede, delete of a pinned row).

### Search semantics

`memory_search` is backed by SQLite FTS5 with the `unicode61` tokenizer.

- Each whitespace-separated token becomes a prefix match, AND'd with the
  others. `caddy waf` matches entries containing both `caddy*` and `waf*`,
  ranked by BM25.
- Tokens that contain non-alphanumeric characters are quoted as phrases, so
  queries like `100%` or `"hello` do not break the parser.
- Punctuation in the indexed data (`_`, `-`, `.`, `:`, `/`, etc.) is a
  separator at index time, so `repo:caddy-waf` is matched by `caddy`,
  `waf`, or `repo`.
- Results order: FTS5 rank, then `updated_at DESC`.
- A query that fails to parse as FTS5 falls back to a case-insensitive
  `LIKE` substring scan with `%` and `_` treated literally.

### Hybrid retrieval

FTS5 only matches literal tokens and their prefixes, so a cross-lingual or
paraphrased query that shares no token with its target returns nothing —
`idee per migliorare la memoria` finds no match for a semantically perfect
English memory. Set `LTM_EMBEDDING_URL` (and `LTM_EMBEDDING_MODEL`) to an
OpenAI-compatible `/v1/embeddings` endpoint — Ollama, LM Studio, vLLM,
llmproxy, or OpenAI itself — to turn on hybrid retrieval:

- On every `memory_save`, the value is embedded and the vector stored in
  the same row. Best-effort: a failed embed never blocks the save, only the
  vector is skipped.
- `memory_search` then runs FTS5 **and** a flat cosine vector search and
  blends the two rankings with Reciprocal Rank Fusion (k=60). Pinned status
  is only a tie-breaker on equal RRF score, not an override.
- With the env unset (or `LTM_EMBED_DISABLE=1`) the vector path is skipped
  entirely and search is pure FTS5 — no behavioural change from earlier
  versions.

Existing memories are not embedded retroactively. After enabling the
endpoint, run `ltm reembed` once to backfill; subsequent saves auto-embed.

| Variable              | Default | Description |
|-----------------------|---------|-------------|
| `LTM_EMBEDDING_URL`   | (empty) | OpenAI-compatible `/v1/embeddings` base URL. Empty = FTS-only. |
| `LTM_EMBEDDING_MODEL` | (empty) | Embedding model name passed to the endpoint. |
| `LTM_EMBED_DISABLE`   | (empty) | `1` forces the vector path off even when a URL is set. |
| `LTM_EMBED_TIMEOUT`   | `5s`    | Per-request timeout (Go duration). |

### Compact responses

`memory_get` returns `{key, scope, tags, pinned, archived?, size_bytes,
schema | preview, hint, verified_at?, staleness_days?, origin?,
created_at, updated_at, compact: true}` by default. The `value` field is
omitted. Pass `expand:true` to get the full record.

`memory_search` likewise returns `SearchHit` objects without the value, but
with a snippet (FTS5 `snippet()` with `<<…>>` markers) and a score
(`-bm25()`, larger is more relevant). In most cases the snippet shows what
the caller needed without a follow-up `memory_get`.

The CLI commands (`ltm get`, `ltm search`) always return the full record.

### Scopes

A memory is identified by `(scope, key)`. The same `key` can live
independently in different scopes — `(user, focus)` and
`(repo:l0-memory, focus)` are different rows. `memory_search`,
`memory_list`, and the resource list accept `scope` to restrict; omit it
to query every scope at once. `memory_save`, `memory_get`, `memory_delete`
default to `user`.

Conventional scope names:

- `user` — cross-project notes.
- `repo:<name>` — repo-specific context.
- `desktop`, `code`, etc. — host-specific notes when the same store is
  shared across multiple MCP hosts.

### Pinning and freshness

Three orthogonal signals can be attached to a memory:

- **Pinned** (`memory_pin {pinned:true}`) — surfaced first by `memory_list`
  and exposed as an MCP resource. Pinning sets `verified_at = now`.
- **Verified** (`memory_verify`) — `verified_at` is updated; compact views
  expose `staleness_days = (now - verified_at) / 1 day`. The host can use
  this to skip or flag old memories.
- **Archived** — set by `memory_supersede`. Archived rows are hidden from
  `memory_list` and `memory_search` by default but stay queryable
  (`memory_get` still returns them). The graph keeps incident links, so a
  successor can be reached from old references.

### Knowledge graph

Edges are typed and directional. The triple `(from, to, rel)` is unique:
re-linking the same triple is a no-op. Edges respect a foreign-key cascade,
so deleting a memory drops every edge incident to it. Edges may cross
scope boundaries.

```sh
ltm save tech:caddy "Caddy server" tech
ltm save repo:caddy-waf "WAF plugin" repo
ltm link repo:caddy-waf depends_on tech:caddy

ltm traverse tech:caddy 2
# {root, depth, nodes:[…], edges:[…]}
```

`memory_traverse` runs a BFS from the given node, deduplicates visits,
filters by `rel` if requested, and supports `direction` `out`, `in`, or
`both`.

## CLI reference

```
ltm [--scope <name>] <command> [args...]
# LTM_SCOPE in the environment has the same effect as --scope.

ltm list [limit]                           # pinned-first, archived hidden
ltm pinned [limit]                         # only pinned entries
ltm get <key>
ltm search <query> [limit]
ltm query <key> [path]                     # JSON Pointer + '*' wildcard
ltm save <key> <value|-> [tags]            # value of "-" reads from stdin
ltm delete <key>
ltm rename <old_key> <new_key>             # cascades through links
ltm verify <key>                           # mark "still current"
ltm supersede <old> <new> <value|-> [tags] # archive old, create new, link supersedes
ltm pin <key>
ltm unpin <key>
ltm link <from_key> <rel> <to_key>         # same-scope edge (cross-scope is via MCP)
ltm unlink <from_key> <rel> <to_key>
ltm links <key>
ltm traverse <key> [depth]                 # JSON: {root, depth, nodes, edges}
ltm reembed [--force]                      # backfill embeddings for hybrid retrieval
ltm path                                   # prints the SQLite DB path
ltm version
```

## VSCode extension

The sidebar has two panes:

- **Pinned** — pinned memories. Pin/unpin context actions.
- **Memories** — full list. The toolbar exposes Add, Search, Filter by
  scope, Toggle group by scope, Sort, Open knowledge graph, Refresh, Clear
  filter, Delete selected. The view title shows the active filters in the
  form `scope:user · q:"caddy" · sort:key · grouped`.

Per-item context actions: Open in editor, Verify, Supersede with new key,
Rename key, Edit, Link to, Show neighbors, Remove a link, Open graph from
here, Pin/Unpin, Delete.

A status bar item on the right (`$(database) l0: N`, plus
`$(pinned) K` when pinned > 0) shows totals and clicks back to the
sidebar.

### Knowledge graph viewer

The `Open knowledge graph` button (toolbar of the Memories pane, or
per-item context action) launches a side webview that renders the store
as a D3 force-directed graph. Nodes are coloured by scope class; pinned
nodes have a stronger outline; the root of a per-item graph has the
thickest outline. Click a node to open the memory; double-click to
re-root; drag to reposition; scroll to zoom. Depth (1–4) and direction
(`out`/`in`/`both`) selectors trigger a re-fetch via `memory_traverse`.
D3 is bundled locally (no CDN).

### Settings

| Setting                  | Default     | Description |
|--------------------------|-------------|-------------|
| `l0-memory.binaryPath`   | `""`        | Absolute path to `ltm`. Empty enables auto-discovery. |
| `l0-memory.dbPath`       | `""`        | Override SQLite DB path (sets `LTM_DB`). |
| `l0-memory.defaultScope` | `"user"`    | `user` / `ask` / `repo:current`. Used when adding from the sidebar. |
| `l0-memory.groupByScope` | `false`     | Group memories under collapsible scope nodes in the Memories tree. |
| `l0-memory.sortBy`       | `"updated"` | `updated` / `created` / `key` / `scope`. Pinned items always come first. |
| `l0-memory.autoStartMCP` | `false`     | Spawn the MCP server in the background on activation. Usually unnecessary because the host starts it. |

### Binary auto-discovery

When `l0-memory.binaryPath` is empty, the extension searches in this order:

1. The bundled binary inside the extension at `bin/<goos>-<goarch>/ltm`.
2. The dev layout (`../server/ltm` relative to the extension folder, when
   running from this repository).
3. Common install locations: `/usr/local/bin/ltm`,
   `/opt/homebrew/bin/ltm`, `~/.local/bin/ltm`, `~/go/bin/ltm`.
4. `ltm` resolved via `PATH`.

If none of the above resolves to an executable, the sidebar surfaces an
error with two actions: open the `binaryPath` setting, or open the
output channel.

## Diagnostics

`ltm` honours two diagnostic environment variables:

- `LTM_DEBUG=1` — timestamped lines on stderr (boot, OpenStore success,
  every JSON-RPC line read, EOF, scanner errors).
- `LTM_LOG_FILE=/path/to/file` — same lines appended to `file` (and
  implies `LTM_DEBUG`). Useful when the host does not forward subprocess
  stderr to its own log.

## Development

```sh
make build        # server/ltm
make test         # go vet + go test -race in server/
make vsix         # full extension build, including cross-compiled binaries
make clean
```

CI runs the same checks on Linux/macOS/Windows × Go 1.22 and 1.23, plus
the extension compile on Node 20 and 22. The release workflow is
tag-driven (`v*`); it cross-compiles the five platform binaries with
ad-hoc codesign on the macOS targets, packages the extension with
embedded binaries, and uploads everything as a GitHub release.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution process and
[SECURITY.md](SECURITY.md) for the threat model and the macOS provenance
gate troubleshooting.

## License

MIT — see [LICENSE](LICENSE).
