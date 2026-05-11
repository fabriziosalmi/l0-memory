# VS Code Extension

The **l0-memory VS Code extension** provides a powerful graphical interface to manage and visualize your memories. It complements the MCP tools by offering a human-friendly way to inspect and edit the store.

## Sidebar Panes

The extension adds an **l0-memory** activity bar icon, which opens a sidebar with two primary views:

### 1. Pinned
Displays all memories you have marked as pinned. This view provides quick access to your most important context. You can unpin items directly from this pane.

### 2. Memories
A complete, searchable list of all memories (excluding archived ones).
- **Toolbar Actions:** Add new memory, Search, Filter by scope, Toggle grouping by scope, Change sort order, and Refresh.
- **Grouping:** Group memories by scope for easier navigation in large stores.
- **Sorting:** Sort by recently updated, created, or alphabetically by key.

## Context Actions

Right-clicking an item in the tree provides a rich set of actions:
- **Open in Editor:** Edit the memory value as a standard text file.
- **Verify:** Mark the memory as current.
- **Supersede:** Replace the key with a new one while archiving the old record.
- **Knowledge Graph:** Open the graph viewer rooted at this specific memory.
- **Link to...:** Interactively create a typed link to another memory.

## Knowledge Graph Viewer

The **Graph Viewer** is a side-by-side webview that renders your memory store as a D3.js force-directed graph.

- **Dynamic Interaction:** Nodes represent memories, and edges represent links.
- **Navigation:** Click a node to focus it in the sidebar, or double-click to re-root the graph traversal.
- **Controls:** Adjust the traversal depth and direction directly from the graph UI.

## Status Bar

A status bar item (usually on the right) displays the total count of memories (`$(database) l0: N`) and the number of pinned items (`$(pinned) K`). Clicking this item quickly reveals the l0-memory sidebar.

## Settings & Discovery

The extension automatically manages the lifecycle of the `ltm` binary.
- **Auto-Discovery:** It searches common paths (`/usr/local/bin`, `~/.local/bin`, etc.) and the extension's own bundled binaries.
- **Configuration:** You can manually set the `binaryPath` or `dbPath` in VS Code settings if you use a custom setup.
