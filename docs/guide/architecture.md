# Architecture

**l0-memory** is designed to be a lightweight, local-first, and highly reliable memory store for AI assistants.

## Core Components

### 1. The `ltm` Binary
A pure Go binary that serves two roles:
- **CLI Tool:** Direct interaction with the SQLite database.
- **MCP Server:** Speaks the Model Context Protocol over stdio.

### 2. SQLite Database
All memories are stored in a local SQLite file. This ensures:
- **ACID Compliance:** Data integrity is guaranteed.
- **Performance:** Fast lookups and full-text search.
- **Portability:** The database is just a single file.

### 3. VS Code Extension
Provides a graphical interface to interact with the memories, including a specialized TreeView and a D3.js knowledge graph visualizer.

## Key Concepts

### Scopes
Memories are partitioned by `(scope, key)`. This allows the same key to exist in different contexts:
- `user`: Global notes available everywhere.
- `repo:<name>`: Context specific to a project.
- `desktop`: Specific to a machine or host.

### Full-Text Search (FTS5)
Search is powered by SQLite's FTS5 extension with the `unicode61` tokenizer. It supports prefix matching and ranks results using BM25.

### Knowledge Graph
Memories can be linked using typed, directional edges. This creates a graph structure that AI assistants can traverse to understand relationships between disparate pieces of information.

### Freshness & Pinning
- **Pinned:** Surfaced first and exposed as MCP resources.
- **Verified:** Timestamps when a memory was last confirmed as accurate.
- **Archived:** Hidden from default views but preserved for historical context.

## Security

**l0-memory** prioritizes privacy:
- **Local Only:** No network listener, no telemetry, no cloud sync.
- **Plaintext:** Data is stored as provided, allowing easy inspection and backup.
- **Provenance:** `origin` and `origin_agent` fields track which tool or model created the memory.
