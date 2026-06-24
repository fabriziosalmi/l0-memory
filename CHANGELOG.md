# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project follows
[Semantic Versioning](https://semver.org/).

## [0.7.0] - 2026-05-11

Documentation site. A VitePress guide + reference published to GitHub Pages
from `docs/`, covering install, MCP-host integration, the knowledge graph,
scopes/freshness, the CLI, the MCP tools, and configuration. No runtime or
schema changes — the `ltm` binary and its store are identical to v0.6.0.

### Added
- `docs/` VitePress site (guide + reference) with local search, an
  Apple-like theme, and Lucide-style SVG iconography.
- GitHub Pages deployment workflow that builds and publishes the site.

### Changed
- Version bumped to 0.7.0 in the Makefile and the VS Code extension
  manifest (`extension/package.json`).

## [0.6.0] - 2026-05-09

Hybrid retrieval. Targets the "literal-token-only search" failure mode by
adding an embedding layer alongside FTS5: a query in one language can
now find a memory whose body is in another, and a query that doesn't
share any token with its target can still surface it via cosine
similarity. The smoking-gun baseline ("idee per migliorare la memoria"
returning 0 hits despite a semantically-perfect English memory) was
captured before the work and flips to a top-1 result after.

### Added — schema (additive, no rebuild)
- `embedding BLOB` nullable — little-endian float32, 4 bytes per dim,
  no header, dim inferred from `len(blob) / 4`. Null on rows that have
  not been embedded yet (endpoint down at save time, pre-0.6 rows
  awaiting backfill).
- `embedding_model TEXT NOT NULL DEFAULT ''` — records which model
  produced the BLOB so a later model swap is detectable without
  per-row metadata. Search-time dim mismatch silently skips the row.
- Migration is plain `ALTER TABLE ADD COLUMN` for v0.5 → v0.6,
  appended to the same transaction as the v0.5 adds.

### Added — embed client (server/embed.go)
- OpenAI-compatible `/v1/embeddings` client. Single implementation
  works against Ollama, llmproxy, LM Studio, vLLM, and OpenAI proper.
- Env-driven config:
  - `LTM_EMBEDDING_URL`   (empty = disabled, FTS-only path)
  - `LTM_EMBEDDING_MODEL`
  - `LTM_EMBED_DISABLE=1` (force-off even when URL is set)
  - `LTM_EMBED_TIMEOUT`   (Go duration, default 5s)
- Per-request `context.WithTimeout` honours both caller ctx and the
  configured timeout — whichever fires first wins.

### Added — Store API
- `SetEmbedding(ctx, scope, key, vec, model)` — UPDATE-only. Does NOT
  touch `updated_at`; the embedding is an index byproduct, not a
  content edit.
- `GetEmbedding(ctx, scope, key)` — returns `(vec, model, err)` with
  `(nil, "", nil)` on rows that have not been embedded yet.
- `VectorSearch(ctx, scope, qvec, limit)` — flat cosine over rows
  with embeddings. Skips archived rows and rows whose stored
  embedding has a different dimensionality than the query vector
  (silent model-swap safety).
- `Search` and `SearchExpanded` consult both FTS and the vector path
  (when an embed client is attached) and blend ranks via Reciprocal
  Rank Fusion (k=60). Pinned acts as a tie-breaker on equal RRF
  scores, NOT an override — semantic relevance is the primary
  query-time signal.

### Added — auto-embed on save
- `Store.SetEmbedClient(c)` attaches an `EmbedClient`. When set,
  `Save` / `SaveWithOptions` call `Embed` synchronously after the
  SQL insert/update commits and persist the vector via
  `SetEmbedding`.
- Best-effort: a failed embed is logged to stderr and swallowed.
  The contract of Save is "the row is durably stored" — never
  "the row is durably embedded". Embedding can be reconstructed
  via `ltm reembed`; persistence cannot.
- `OpenStore` (the production entry point) auto-attaches an
  env-driven client. Tests that don't need embeds use `openStoreAt`
  directly and stay zero-config.

### Added — CLI
- `ltm [--scope X] reembed [--force]`. Backfills missing
  embeddings (or all rows when `--force`). Per-row errors are
  collected without aborting; a transient endpoint hiccup on row 7
  doesn't lose work done on rows 1–6. Refuses to run with the
  disabled client.

### Added — search ranking (pre-hybrid groundwork)
- `memory_search` now sorts pinned memories first within tier in
  the FTS path. `memory_list` already did this since v0.2.0; the
  search semantics now match. Within a tier, BM25 then `updated_at`
  then `id` — order otherwise unchanged.

### Fixed
- `memory_traverse` with `direction=both` was emitting incident
  edges twice — once when visiting the from-node (as DirOut) and
  again when visiting the to-node (as DirIn, reconstructed back to
  the same `(from, to, rel)` triple). Verified live against
  production data 2026-05-09 (a real two-hop traverse showed the
  `derived_from_session` edge twice). Fix: dedup-set on
  `(from, to, rel)` before append. Zero behaviour change for
  `direction=out|in` — the bug only surfaced in two-direction
  traversal.

### Build
- New `make install` target: builds, copies the binary to
  `~/.local/bin/ltm`, applies an ad-hoc `codesign` (macOS), and
  prints a reminder that the MCP host (Claude Desktop / Claude
  Code) must be quit + relaunched to pick up the new binary.
  This is the iterative re-deploy seam — the file half — leaving
  the user the restart half.
- `install-mcp` and `install-mcp-desktop` now depend on `install`
  and register the canonical `~/.local/bin/ltm` path instead of
  the dev-checkout `$(CURDIR)/server/ltm`.
- `install-mcp-desktop` jq now MERGES into any existing
  `mcpServers.l0-memory` block (`|= ((. // {}) + {…})`) instead
  of hard-replacing it. Operator-set keys like `env`
  (`LTM_EMBEDDING_URL`, etc.) are preserved across re-runs.

### Tests
- 96 → 133 (+37 under `-race`). Coverage: encode/decode round-trip
  including malformed-payload rejection; embed client (disabled,
  env-disable flag, happy path with httptest, empty input, HTTP
  5xx, empty data, timeout); schema v0.6 columns on fresh DB;
  SetEmbedding round-trip + ErrNotFound + null-by-default +
  updated_at-not-touched invariant; reembedAll with skip-already-
  embedded / force / scope filter / per-row error continuation;
  Save auto-embed happy + failure + missing-client + disabled-
  client + update-replaces-vec; cosine basics including dim
  mismatch; VectorSearch top-K + skip-unembedded-and-archived +
  scope filter + dim-mismatch skip; rrfBlend score arithmetic +
  exclusive-hit union + limit; cross-lingual hybrid end-to-end;
  FTS fallback when embed fails; pure-FTS when no client; hybrid
  doesn't regress token-exact #1; traverse dedup regression.

### Migration notes
- v0.5.x DBs upgrade transparently on first open of the v0.6
  binary. No table rebuild; just two ADD COLUMNs in the same
  transaction as the v0.5 adds.
- A v0.5.x binary opening a v0.6 DB continues to work — its
  explicit-column SELECTs ignore the new columns. INSERTs from
  v0.5.x leave the new columns at their defaults (`embedding=NULL`,
  `embedding_model=''`), which is functionally identical to a row
  awaiting backfill.
- Existing memories are not embedded automatically. Run
  `LTM_EMBEDDING_URL=… LTM_EMBEDDING_MODEL=… ltm reembed` once
  after upgrading to populate the index. Subsequent saves
  auto-embed when the env is set.
- For the embedding layer to take effect inside an MCP host, set
  `LTM_EMBEDDING_URL` / `LTM_EMBEDDING_MODEL` in that host's MCP
  server `env` block (Claude Desktop:
  `~/Library/Application Support/Claude/claude_desktop_config.json`;
  Claude Code: `~/.claude.json` under
  `mcpServers.l0-memory.env`).

## [0.5.1] - 2026-05-07

Bugfix release after a draconian audit of the v0.5.0 surface.

### Fixed
- `memory_list` MCP dispatcher: previously `_ = json.Unmarshal(args, &a)`
  swallowed parse errors and silently returned every scope when the
  caller sent a malformed `args` payload. Bad arguments now produce a
  proper JSON-RPC error.
- `backfillFTS` is robust to FTS-index drift: it inserts the missing
  rowids from `memories` and deletes orphan rowids that no longer exist
  in `memories`. The previous version compared `COUNT(*)` totals, which
  could miss persistent inconsistencies in the presence of orphans.
- `migrateSchema` 0.5 additive step now runs inside an explicit
  transaction. A crash mid-migration can no longer leave the schema with
  a partial set of new columns.
- VSCode extension: `startMCP` tracks its `stdout`/`stderr`/`exit`
  listeners and detaches them deterministically on `stopMCP`. Repeated
  start/stop cycles no longer accumulate event listeners on the
  underlying streams.
- VSCode extension: the "Link to…" picker now excludes archived
  memories, in line with `memory_list` / `memory_search` behaviour.

### Documentation
- README: rewritten for accuracy and brevity. Tool table now lists
  required vs optional arguments separately; the `memory_save` row
  documents `origin` and `origin_agent` (added in 0.5.0).
- `extension/README.md`: synchronised with the main README. The settings
  table now includes `defaultScope`, `groupByScope`, `sortBy`.
- README status-bar text matches the actual codicon-based output.
- CHANGELOG entry for 0.2.0: removed an unverifiable claim about the
  previous MCP protocol version (the relevant commits were dropped during
  the v0.1.0 history reset).
- Added a "Diagnostics" section documenting `LTM_DEBUG` and
  `LTM_LOG_FILE`.

## [0.5.0] - 2026-05-07

Freshness signals + provenance + supersession. Targets the "AI cites
stale facts with confidence" failure mode by giving each memory a
last-verified timestamp and a clean way to retire old beliefs.

### Added — schema (additive, no rebuild)
- `verified_at INTEGER NOT NULL DEFAULT 0` — timestamp of the last
  user-confirmed "this is still current". `0` means "never verified".
- `archived INTEGER NOT NULL DEFAULT 0` — soft-delete flag. Archived
  rows stay queryable but are hidden from `memory_list`/`memory_search`
  by default; the graph keeps the historical edges.
- `origin TEXT NOT NULL DEFAULT ''` — free-form provenance hint
  (e.g. `claude-code: session abc`). Set at save time, preserved on
  later updates that don't supply a new value.
- Migration is plain `ALTER TABLE ADD COLUMN` for v0.4 → v0.5; no
  table rebuild required.

### Added — Store API
- `Verify(scope, key)` updates `verified_at = now`. Returns `ErrNotFound`
  when missing.
- `SaveWithOptions(scope, key, value, tags, *SaveOptions)` accepts
  `Origin` and `OriginAgent` (combined as `"<agent>: <origin>"` when
  both are set). The legacy `Save(...)` keeps existing call sites
  working.
- `Supersede(scope, oldKey, newKey, value, tags)` is the canonical way
  to retire a belief: it creates the new memory, archives the old, and
  links new `--supersedes→` old in a single transaction.
- `Pin(...)` now implies `Verify(...)` — pinning is the user telling
  the system "this is still current" by definition.
- `List(...)` / `Search(...)` filter out archived rows. New
  `ListIncludingArchived(...)` for explicit "show me history" queries.

### Added — MCP tools
- `memory_verify {scope?, key}` — calls Verify; compact view returned.
- `memory_supersede {scope?, old_key, new_key, value, tags?}` — atomic
  swap; emits `notifications/resources/list_changed`.
- `memory_save` accepts optional `origin` and `origin_agent`.
- Compact view (`memory_get`, `memory_search` hits) now exposes
  `verified_at`, `staleness_days`, `origin`, `archived`.

### Added — CLI
- `ltm [--scope X] verify <key>`
- `ltm [--scope X] supersede <old> <new> <value|-> [tags]`

### Added — VSCode extension
- New context actions per memory: **Verify (mark current)** and
  **Supersede with new key…** (the latter prompts for new key, value
  pre-filled with the old one, and tags).
- Tooltip now shows `_verified:_` (or "never"), age in days with a
  staleness flag (`fresh` ≤ 7d, `🟡 stale` > 30d), `_origin:_`, and
  an `🗄️` marker on archived items.
- Bumped extension `0.4.0` → `0.5.0`.

### Tests
- 94/94 green under `-race`. New: verify-updates-timestamp, verify-not-
  found, pin-implies-verify, save-with-origin, origin-preserved-on-noop-
  update, supersede-archives-and-links, supersede-refuses-collision,
  archived-hidden-from-list-and-search-by-default, migration v0.4 → v0.5
  with new columns defaulting to zero/empty.

## [0.4.0] - 2026-05-07

Tree polish + rename + power-user UX. Server adds one new operation
(`memory_rename`); the rest is extension UX.

### Added — server
- `Store.Rename(scope, oldKey, newKey)` rewrites the key atomically and
  cascades the change through `memory_links` (both `from_key` and
  `to_key`). Uses `PRAGMA defer_foreign_keys = ON` inside the
  transaction so the composite FK is checked at commit time.
- `memory_rename` MCP tool + `ltm [--scope X] rename <old> <new>` CLI
  subcommand. Returns `ErrNotFound` for missing source, errors on
  destination collision; same-key call is a no-op.

### Added — extension UX
- **Rename key…** context action on every tree item (calls `ltm rename`).
- **View title indicator**: when filter / scope / sort / group are active,
  the Memories tree shows them in the view's description, e.g.
  `scope:user · q:"caddy" · sort:key · grouped`.
- **Differentiated icons**: pinned → `pinned`, JSON-valued →
  `symbol-namespace`, plain text → `symbol-text` (was the generic
  `symbol-key`).
- **Group by scope** toggle (toolbar) backed by `l0-memory.groupByScope`
  setting. When on, the tree groups memories under collapsible scope
  nodes (`user`, `feedback`, `repo:*`). Disabled when scope filter is
  already restricting the view.
- **Sort options** quickPick (toolbar) backed by `l0-memory.sortBy`:
  `updated` (default) / `created` / `key` / `scope`. Pinned memories
  always come first within any sort.
- **Bulk delete**: the Memories tree is now multi-selectable
  (`canSelectMany: true`). Toolbar overflow `Delete selected memories`
  walks the selection with a single confirmation prompt.

### Tooltip
- Now shows _scope_, _tags_, _pinned_, _size_, _updated_ on every memory.
  Body preview truncated at 400 chars.

### Bumped
- VSCode extension `0.3.0` → `0.4.0`.

## [0.3.0] - 2026-05-07

VSCode extension UX overhaul. Server is unchanged from 0.2.0; only the
extension and a few CLI ergonomics moved.

### Added — knowledge graph webview
- New command `l0-memory: Open knowledge graph` (toolbar button on the
  Memories pane). Renders the entire memory store as a D3 force-directed
  graph in a side webview, with nodes coloured by scope and pinned
  memories highlighted.
- New context action `l0-memory: Open graph from here` on each memory in
  the tree — opens the same webview centred on that node, with a depth
  selector (1–4) and direction filter (out/in/both) wired to
  `memory_traverse`.
- In-graph interaction: click a node to open its memory in the editor;
  double-click to re-root the graph on that node; drag to reposition;
  scroll to zoom; "reset zoom" toolbar button. D3 v7 ships as a local
  webview asset (`extension/media/d3.v7.min.js`) — no CDN, CSP-safe.

### Added — link / traverse UI
- `l0-memory: Link to…` (context action) — pick a target memory from a
  quickPick (filtered by tags + value preview), then enter a relationship
  label. Same-scope edges only via the UI; cross-scope is via MCP.
- `l0-memory: Show neighbors` — opens a markdown summary of every link
  incident to the selected memory, with arrow direction.
- `l0-memory: Remove a link…` — quickPick of incident edges, pick one to
  delete.

### Added — scope-aware UX
- New setting `l0-memory.defaultScope`: `"user"` (default), `"ask"`, or
  `"repo:current"`. `"ask"` opens a quickPick of existing scopes plus a
  `+ New scope…` entry; `"repo:current"` derives the scope from the open
  workspace folder name.
- New toolbar button `l0-memory: Filter by scope` on the Memories pane,
  with a quickPick of every scope currently present in the store.
- Tree labels show `<scope>/<key>` for non-default scopes so the same key
  in different scopes is visually distinguishable.

### Added — quick wins
- Status bar indicator: `$(database) l0: <total> ($(pinned) <pinned>)`
  on the right side of the bar; click focuses the Memories pane.
- `Open in editor` now detects JSON-valued memories and opens them with
  `language: "json"` (folding + outline + syntax highlight) instead of
  the markdown wrapper.

### Added — server
- New `ltm pinned [limit]` CLI subcommand backing the Pinned pane and
  exposing the same filter at the shell.

### Bumped
- VSCode extension `0.2.0` → `0.3.0`.

## [0.2.0] - 2026-05-07

The "knowledge graph" release. Adds three orthogonal capabilities that
turn l0-memory from a flat KV+search store into a curated, navigable
context surface for any MCP host (Claude Code, Claude Desktop, Cursor,
Cline, …).

### Added — scope as first-class column
- `memories` now has a `scope` text column (default `"user"`) with
  `UNIQUE(scope, key)` replacing the old key-only uniqueness, so the
  same raw key can coexist in `user`, `repo:l0-memory`, etc.
- All MCP tools take an optional `scope`. Save/get/delete default to
  `"user"`; list/search treat empty scope as "all scopes".
- CLI: `ltm [--scope <name>] …` (and `LTM_SCOPE` env). Default behaviour
  unchanged for existing scripts.
- Automatic, idempotent migration from v0.1.x DBs (rebuild table inside
  a transaction, FTS index preserved).

### Added — pin + MCP resources
- `pinned` integer column on memories. `memory_list` sorts pinned first.
- `memory_pin {scope?, key, pinned}` MCP tool + `ltm pin` / `ltm unpin`
  CLI commands + `ltm pinned` listing alias.
- Pinned memories are exposed as MCP **resources** at
  `memory:///<scope>/<key>` so MCP hosts attach them to context with
  zero round-trips. `notifications/resources/list_changed` is emitted
  on every pin/unpin/delete that may change the set.
- VSCode extension grew a "Pinned" TreeView above "Memories" with Pin /
  Unpin context actions. Non-default scopes are shown as `scope/key` to
  disambiguate. extension bumped to **0.2.0**.

### Added — knowledge-graph layer (memory_link)
- New `memory_links` table with composite FKs to `memories(scope, key)`
  and `ON DELETE CASCADE`. The `(from, to, rel)` triple is unique;
  re-linking is a no-op.
- Store API: `Link`, `Unlink`, `Links`, `Neighbors(rel, direction)`,
  `Traverse(depth, rel, direction)` returning `{root, depth, nodes,
  edges}` with cycle detection.
- MCP tools `memory_link`, `memory_unlink`, `memory_links`,
  `memory_traverse`. Cross-scope edges work — `repo:waf/main`
  `--uses→` `user/tech:caddy`.
- CLI: `ltm link`, `ltm unlink`, `ltm links`, `ltm traverse [depth]`.

### Tests
- 80/80 green under `-race`. New coverage: scope migration,
  same-key-different-scope coexistence, pin toggle + ListPinned,
  memory_pin round-trip, resources/list (only pinned + scope display),
  resources/read content + 404, list_changed notification, link CRUD,
  cascade on memory delete, traverse depth + cycle + cross-scope.

### Fixed
- **macOS provenance gate**: `make build`, the extension's
  `scripts/build-bins.sh`, and the release workflow now apply an ad-hoc
  `codesign` to every darwin output. Without this, macOS Sequoia/Tahoe
  silently kills unsigned subprocesses spawned by signed apps (Claude
  Desktop, Cursor) — manifest as `Server transport closed unexpectedly`
  ~25 ms after start, with no log on stderr or stdout. See
  SECURITY.md → "macOS provenance gate" for the manual workaround.

### Added (debug)
- `LTM_DEBUG=1` enables timestamped diagnostic lines on stderr.
- `LTM_LOG_FILE=/path/to/log` mirrors them to a file (and implies
  `LTM_DEBUG`). Useful for hosts that don't forward subprocess stderr to
  their UI logs.

### Migration notes
- Existing v0.1.x databases are upgraded transparently on first open of
  the new binary. The first start may take a fraction of a second on
  large stores while the table is rebuilt.
- MCP clients that previously assumed `memory_get` always returned a
  `value` field still need `expand:true` (introduced in v0.1.0). No new
  breaking changes in this release.

## [0.1.0] - 2026-05-07

First public release. Highlights:

- Single Go binary `ltm` that doubles as a stdio MCP server and a CLI for a
  shared SQLite store; pure Go SQLite (`modernc.org/sqlite`), no CGO.
- Full-text search backed by SQLite FTS5 with a friendly tokenizer
  (whitespace-separated tokens become prefix-matched, AND'd; phrases for
  special characters) and a defensive LIKE fallback.
- Compact-by-default MCP responses — `memory_get` returns metadata + schema
  or preview, `memory_search` returns hits with `<<…>>` snippet and BM25
  score; pass `expand:true` for full records.
- `memory_query` MCP tool / `ltm query` CLI command for slicing JSON-valued
  memories with JSON Pointer (RFC 6901) plus a `*` wildcard segment.
- VSCode extension with platform-specific bundled binaries
  (darwin-arm64/amd64, linux-arm64/amd64, windows-amd64), auto-discovery,
  Open-in-editor / Clear-filter commands, and a friendly error UX when the
  binary cannot be located.
- GitHub Actions CI matrix (Linux/macOS/Windows × Go 1.22/1.23, Node 20/22)
  and a tag-driven release pipeline that publishes binaries and the `.vsix`.
- 51 tests covering store, MCP stdio, JSON Pointer evaluation, and compact
  view helpers.

### Changed (relative to the prototype)
- **`memory_search` is now compact by default** (MCP only — CLI behaviour
  unchanged): the response is an array of compact hits
  `{key, tags, score, snippet, size_bytes, created_at, updated_at}`. The
  `snippet` is produced by FTS5's `snippet()` and surrounds the match with
  `<<...>>` markers; `score` is `-bm25()` so larger = more relevant. Pass
  `expand:true` to get full Memory records. The LIKE-fallback path produces
  the same shape with a Go-side highlight and `score=0`.
- **`memory_get` is now compact by default** (MCP only — CLI behaviour unchanged):
  the response is `{key, tags, size_bytes, is_json, schema|preview, hint, created_at, updated_at}`
  — the `value` field is omitted unless the caller passes `expand:true`.
  - For JSON-valued memories the response includes a small `schema`
    (top-level keys for objects, length for arrays).
  - For text it includes a `preview` clipped to ~240 bytes on a word boundary,
    keeping UTF-8 valid.
  - This is a deliberate breaking change for MCP consumers that previously
    relied on `value` always being present in `memory_get`. Migrate by
    passing `expand:true`, or — better — switch to `memory_query` for slices.

### Added
- **`memory_query` MCP tool + `ltm query` CLI command**: navigate JSON-valued
  memories with a JSON Pointer (RFC 6901) path. The path syntax is extended
  with a `*` wildcard segment that fans out over arrays and objects.
  Eliminates the need to load and parse a multi-KB graph blob client-side
  whenever you only need a slice. Examples:
  - `path=""` or `"/"` → whole document.
  - `path="/clusters/web-security-caddy/members"` → array of repo names.
  - `path="/repos/*/n"` → all repo names from a list of objects.
  Stdlib only (no new dependencies).
- **FTS5-backed search**. `memory_search` (and `ltm search`) now uses a
  SQLite FTS5 virtual table with the `unicode61` tokenizer, ranked by BM25.
  - Free-form queries are tokenized: each whitespace-separated token becomes
    a prefix match AND'd with the others. Example: `caddy waf` matches
    entries containing both `caddy*` and `waf*` in any order, ranked by
    relevance.
  - Tokens with non-alphanumeric characters are quoted as FTS5 phrases.
  - The previous `LIKE` substring search is kept as a fallback for queries
    that fail FTS parsing, so behaviour stays predictable for edge cases.
  - DBs created before this version are auto-upgraded on open: the FTS index
    is backfilled the first time `OpenStore` runs.
- VSCode extension now bundles platform-specific `ltm` binaries
  (`darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`,
  `windows-amd64`) so it works without manual setup after `code --install-extension`.
- `extension/scripts/build-bins.sh` cross-compiles all bundled targets.
- Friendly error UX in the extension when the `ltm` binary cannot be located
  (offers a "Set binary path" / "Open output" action).
- Auto-discovery of `ltm` in common install locations
  (`/usr/local/bin`, `/opt/homebrew/bin`, `~/.local/bin`, `~/go/bin`).
- `ltm version` / `ltm --version` flag, and `Version` is injected at build time
  via `-ldflags="-X main.Version=…"`.
- Graceful shutdown: the MCP server cancels in-flight DB operations on
  `SIGINT`/`SIGTERM`.
- Go test suite for the storage layer and a stdio round-trip test for the MCP
  protocol.
- GitHub Actions CI (Go matrix on Linux/macOS/Windows, extension compile) and a
  tag-driven release workflow that publishes binaries and a `.vsix`.
- Root `Makefile` with `build`, `test`, `extension-bins`, `vsix` targets.

### Changed
- `Memory.created_at` / `updated_at` are now stored as **Unix milliseconds**
  (previously seconds) for better ordering granularity. The extension auto-detects
  legacy second-precision values and rescales.
- MCP protocol version declared as `2025-06-18`. Clients that announce a
  newer revision get their version echoed back in the `initialize`
  response.
- `Store` API now takes `context.Context` on every call.
- The MCP server validates required tool arguments (`key`, `query`) and returns
  a structured tool error instead of relying on SQL-level failures.
- Empty result sets are emitted as `[]` instead of `null` from JSON output.
- Search ignores SQL `LIKE` wildcards in user queries (`%` and `_` are treated
  literally via `ESCAPE`).
- `db.SetMaxOpenConns(1)` to avoid `database is locked` under WAL with
  concurrent writers.

### Fixed
- `spawn ltm ENOENT` when the extension was installed from a `.vsix` and no
  `ltm` was on `PATH` — bundled binary is now picked up automatically.

