# What is l0-memory?

**l0-memory** is a high-performance, local-first long-term memory system designed specifically for AI assistants. It provides a persistent, structured, and searchable store for facts, preferences, and relationships that would otherwise be lost between chat sessions.

## The Mission

The goal of l0-memory is to bridge the gap between ephemeral chat history and a permanent knowledge base. By using the **Model Context Protocol (MCP)**, it allows different AI tools (like Claude Code, Cursor, and Claude Desktop) to share a single, unified memory store.

### Why l0-memory?

- **Privacy First:** Your memories stay on your machine. The store is a local SQLite database—no cloud sync, no telemetry, and no third-party APIs (unless you explicitly configure an optional embedding provider).
- **Zero CGO Dependencies:** The server is written in pure Go, making it incredibly fast and easy to cross-compile for any platform without external C libraries.
- **Structured Knowledge:** Unlike simple key-value stores, l0-memory supports **typed links**, allowing your assistant to build a sophisticated knowledge graph of your projects and workflows.
- **Freshness & Trust:** Built-in mechanisms for **pinning**, **verification**, and **archiving** ensure that the assistant always prioritizes current information over stale data.

## Key Capabilities

### Persistence Across Tools
Because l0-memory speaks MCP, a memory saved while working in **Claude Code** is immediately available to your assistant in **VS Code** or **Claude Desktop**. This creates a seamless "second brain" that follows you across your entire development environment.

### Knowledge Graph
Memories aren't just isolated snippets. They can be linked with meaningful relationships like `depends_on`, `implements`, or `supersedes`. This allows the AI to traverse related context, just like a human developer would.

### Flexible Scoping
Memories can be global (`user`), project-specific (`repo:name`), or host-specific (`desktop`). This prevents "context pollution" while still allowing global facts to be available everywhere.

::: tip Use Cases
- Storing project-specific architectural decisions.
- Remembering personal preferences for code style or documentation.
- Tracking the progress of long-running tasks across different sessions.
- Mapping complex relationships between different parts of a codebase.
:::
