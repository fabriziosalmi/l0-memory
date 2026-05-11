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
