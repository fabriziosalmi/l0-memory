# Integration: Claude Desktop

**l0-memory** integrates seamlessly with the Claude Desktop application, providing a graphical chat interface with access to your long-term memory.

## Automatic Configuration

The easiest way to configure Claude Desktop is using the `Makefile` command:

```sh
make install-mcp-desktop
```

This command performs several actions:
1. Locates the Claude Desktop configuration file.
2. Creates a backup of your existing configuration.
3. Adds the `l0-memory` server definition to the `mcpServers` section.

## Manual Configuration

If you prefer to edit the configuration manually, you need to locate the `claude_desktop_config.json` file on your system.

### Configuration Path

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

### Configuration Snippet

Add the following entry to the `mcpServers` object:

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

::: warning Absolute Paths
Claude Desktop requires **absolute paths** for the `command` field. Ensure you provide the full path to your `ltm` binary.
:::

## Restarting Claude Desktop

After modifying the configuration, you must **completely quit and restart** Claude Desktop for the changes to take effect.

Once restarted, look for the "hammer" icon or check the tool list to ensure the `memory_` tools are available.

## Troubleshooting

If the tools do not appear:
1. Verify the path to the `ltm` binary is correct.
2. Check the Claude Desktop logs for any MCP connection errors.
3. Ensure no other instance of `ltm` is locking the database (though SQLite handles concurrent reads well, the MCP server requires a stable stdio connection).
