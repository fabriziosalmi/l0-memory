# Integration: Claude Code

Integrating **l0-memory** with [Claude Code](https://claude.ai/code) allows your terminal-based assistant to save and retrieve memories directly while you work.

## Automatic Installation

If you have the source code cloned, you can use the provided `Makefile` to quickly add the server to Claude Code:

```sh
make install-mcp
```

This command runs `claude mcp add` with the correct path to your local `ltm` binary.

## Manual Installation

To manually add l0-memory to Claude Code, use the following command:

```sh
claude mcp add l0-memory /absolute/path/to/ltm mcp
```

::: info Note
Replace `/absolute/path/to/ltm` with the actual path where you installed the binary. If it's in your `PATH`, you can just use `ltm`.
:::

## Verification

Once added, you can verify that Claude Code recognizes the new tools:

1. Start Claude Code: `claude`
2. Ask it to list its tools: "What MCP tools do you have access to?"
3. You should see tools prefixed with `memory_` (e.g., `memory_save`, `memory_get`).

## Usage in Claude Code

Claude Code will automatically use these tools when it identifies information worth remembering. You can also explicitly command it:

> "Remember that this project uses a hexagonal architecture."

The assistant will then call `memory_save` to persist this fact in the current scope.

::: tip Scope Awareness
Claude Code is generally aware of your current directory. It will often default to a `repo:` scope if it can identify the repository name, keeping your project memories organized.
:::

## Automatic recall + `/checkpoint`

The MCP server above lets the assistant read and write memories *on request*. The
optional **Claude Code integration** (`integrations/claude-code/`) makes it
automatic — so a session resumes without you re-explaining who you are or where
the project left off:

| Piece | What it does | Trigger |
|-------|--------------|---------|
| Recall hook | A `SessionStart` hook that injects your **persona** (pinned `user`-scope entries) and the current **project** memory (`repo:<slug>`, pinned first) into the session. | Automatic, every session |
| `/checkpoint` skill | Synthesizes a durable state snapshot and saves it to the right scope, pinning the durable one. | You run `/checkpoint` |

Install both with:

```sh
make install-claude
# or: integrations/claude-code/install.sh
```

This copies the hook and skill into `~/.claude/` and registers the hook in
`~/.claude/settings.json` (idempotent). Open a **new** session to pick it up.

::: warning You own the hook
Registering a `SessionStart` hook makes a script run on every session start. The
installer is something **you** run, so you own that change — review
`integrations/claude-code/l0-recall.py` first if you like. The hook reads via the
`ltm` CLI (`pinned` / `list` are pure SQLite reads), truncates each entry, and
fails open (any error injects nothing and never blocks a session).
:::

### The loop

1. Open a repo → the hook injects "who you are" + "this project's state".
2. Work.
3. `/checkpoint` at the end → it saves the updated state, pinning the durable entry.
4. Next session — even from another MCP host — starts at step 1, context already loaded.

### Pinning is the relevance lever

The recall hook surfaces **pinned** entries first and falls back to recent ones.
Pin what you always want on open; leave transient working notes unpinned.

```sh
ltm --scope user        pin identity       # persona facts, surfaced everywhere
ltm --scope repo:myproj pin release-state  # the canonical project state
```

See [`integrations/claude-code/README.md`](https://github.com/fabriziosalmi/l0-memory/tree/main/integrations/claude-code) for configuration (`LTM_BIN`, `L0_RECALL_MAXVALUE`, `L0_RECALL_MAXREPO`).
