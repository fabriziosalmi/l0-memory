# l0-memory :: Claude Code integration

Two pieces that turn the l0-memory MCP server into an **automatic** memory for
[Claude Code](https://claude.ai/code) — so a session resumes without you
re-explaining who you are or where the project left off:

| Piece | What it does | Trigger |
|-------|--------------|---------|
| `l0-recall.py` | SessionStart hook. Injects your **persona** (pinned `user`-scope entries) + the current **project** memory (`repo:<slug>`, pinned first) as context. | Automatic, every session |
| `skills/checkpoint/SKILL.md` | `/checkpoint` skill. Synthesizes a durable state snapshot and saves it to the right scope, pinning the durable one. Shows a diff and asks confirmation before every write — agent proposes, you dispose. | You run `/checkpoint` |

This builds on the MCP server you install with `make install-mcp`; the recall
hook reads the same store directly through the `ltm` CLI, so it is fast and
works offline (no embedding endpoint needed for `pinned` / `list`).

## Install

```sh
make install-claude          # from the repo root
# or, directly:
integrations/claude-code/install.sh
```

The installer copies the hook and skill into `~/.claude/` and registers the
SessionStart hook in `~/.claude/settings.json` (idempotent — it skips if the
hook is already registered there or in `settings.local.json`). Override the
target with `CLAUDE_CONFIG_DIR`. Open a **new** Claude Code session to pick it up.

> Registering a hook makes a script run on every session start. The installer
> is something **you** run, so you own that change; review `l0-recall.py` first
> if you like.

## The loop

1. Open a repo → the hook injects "who you are" + "this project's state".
2. Work.
3. `/checkpoint` at the end → it shows a diff of what it will write, you confirm, then it saves the updated state and pins the durable entry.
4. Next session (even from another MCP host) → step 1 again, context already loaded.

## Pinning is the relevance lever

The recall hook surfaces **pinned** entries first and falls back to recent ones.
Pin the entries you always want on open; leave transient working notes unpinned.

```sh
ltm --scope user        pin identity       # persona facts, surfaced everywhere
ltm --scope repo:myproj pin release-state  # the canonical project state
```

## Configuration

The hook reads these optional environment variables:

| Env | Default | Meaning |
|-----|---------|---------|
| `LTM_BIN` | search `PATH`, then `~/.local/bin/ltm` | path to the `ltm` binary |
| `L0_RECALL_MAXVALUE` | `600` | characters kept per entry (truncation, anti-bloat) |
| `L0_RECALL_MAXREPO` | `6` | max project entries injected (pinned + recent) |

## Notes

- **Scope convention:** `<slug>` is the lowercased basename of the git worktree
  root, matching l0-memory's `repo:<name>` scopes. Outside a git repo, only the
  persona block is injected.
- **Fail-safe:** any error in the hook emits nothing and exits 0 — it never
  blocks a session.
- **Cross-tool:** because every MCP host points at the same SQLite store, a
  `/checkpoint` from Claude Code is visible to Claude Desktop, the VS Code
  extension, and any other host on the next recall.
