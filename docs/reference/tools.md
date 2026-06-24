# MCP Tools Reference

**l0-memory** exposes several tools to MCP hosts. These tools allow the assistant to manage its own long-term memory.

| Tool | Description | Key Arguments |
| :--- | :--- | :--- |
| `memory_save` | Create or update a memory | `key`, `value`, `scope` |
| `memory_get` | Retrieve a specific memory | `key`, `scope`, `expand` |
| `memory_search` | Search all or specific scopes | `query`, `limit` |
| `memory_list` | List recent memories | `scope`, `limit` |
| `memory_delete` | Delete a memory | `key`, `scope` |
| `memory_query` | Slice JSON-valued memory | `key`, `path` |
| `memory_pin` | Toggle pinned status | `key`, `pinned` |
| `memory_link` | Link two memories | `from_key`, `to_key`, `rel` |
| `memory_unlink` | Remove a link | `from_key`, `to_key`, `rel` |
| `memory_links` | List links for a memory | `key`, `scope` |
| `memory_traverse` | BFS subgraph view | `key`, `depth`, `direction` |
| `memory_rename` | Atomic rename | `old_key`, `new_key` |
| `memory_verify` | Update freshness | `key`, `scope` |
| `memory_supersede` | Archive and replace | `old_key`, `new_key`, `value` |

## Tool Details

### `memory_save`
Inserts or updates a memory identified by `(scope, key)`. The assistant can also provide `tags`, `origin`, and `origin_agent` to track how the memory was created.

### `memory_search`
Uses SQLite FTS5 for efficient prefix matching across keys, values, and tags. By default, it returns a compact representation with a text snippet and a relevance score. When an embedding endpoint is configured it becomes **hybrid**, blending FTS5 with vector similarity via Reciprocal Rank Fusion — see [Hybrid Retrieval](/guide/features-hybrid).

### `memory_query`
Allows extracting specific parts of a JSON-valued memory using JSON Pointers (RFC 6901). Supports `*` wildcards for array/object traversal.

### `memory_traverse`
Enables the assistant to explore the knowledge graph. It returns a set of nodes and edges within a specified depth from the starting memory.
