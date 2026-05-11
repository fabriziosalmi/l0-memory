# Integration: Other MCP Hosts

Since **l0-memory** is a standard Model Context Protocol (MCP) server, it can be used with any host that supports the protocol, such as **Cursor**, **Cline**, and more.

## General Configuration

Most MCP hosts follow a similar configuration pattern. You generally need to provide the command to run the server and any necessary arguments.

### Required Settings

- **Command:** The absolute path to the `ltm` binary.
- **Arguments:** `["mcp"]`
- **Environment Variables (Optional):** You may want to set `LTM_DB` if your database is in a non-standard location.

### JSON Configuration Example

For hosts that use a JSON configuration file (like Cursor or many IDE extensions), use this structure:

```json
{
  "mcpServers": {
    "l0-memory": {
      "command": "/usr/local/bin/ltm",
      "args": ["mcp"],
      "env": {
        "LTM_DB": "/Users/yourname/.long-term-memory/memories.db"
      }
    }
  }
}
```

## Cursor

To add l0-memory to Cursor:
1. Open Cursor Settings.
2. Navigate to **Features > MCP**.
3. Click **+ Add New MCP Server**.
4. Set the name to `l0-memory`.
5. Set the type to `command`.
6. Enter the command: `/path/to/ltm mcp` (ensure the path is absolute).

## Cline (formerly Claude Dev)

To add l0-memory to Cline:
1. Open the Cline settings in VS Code.
2. Find the MCP configuration section.
3. Add a new server entry following the JSON structure shown above.

## Custom Integrations

If you are building your own MCP host, l0-memory supports the following standard MCP features:
- **Tools:** A wide range of `memory_*` tools for CRUD and graph operations.
- **Resources:** Pinned memories are exposed as resources at `memory:///<scope>/<key>`.
- **Notifications:** The server sends `notifications/resources/list_changed` when the set of pinned memories changes.
