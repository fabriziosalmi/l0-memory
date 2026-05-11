# Knowledge Graph

One of the most powerful features of **l0-memory** is its ability to organize information as a **Knowledge Graph**. Instead of flat notes, your assistant can build a web of interconnected concepts.

## Typed, Directional Edges

A link in l0-memory is defined as a triple: `(from, to, relationship)`.

- **Directional:** Links have a clear "from" and "to".
- **Typed:** Every link has a `rel` (relationship) string, such as `depends_on`, `implements`, `part_of`, or `related_to`.
- **Unique:** The same triple can only exist once.

### Cross-Scope Linking
Links can bridge different scopes. You can link a `repo:` specific memory to a global `user` memory.

```sh
# Example: Linking a project component to a global technology note
ltm link repo:my-app:auth depends_on user:oauth2-standards
```

## Graph Traversal

AI assistants can "walk" the graph using the `memory_traverse` tool. This allows them to discover related context without knowing the specific keys in advance.

### BFS Algorithm
`memory_traverse` performs a Breadth-First Search (BFS) starting from a root node.

- **Depth:** Control how many levels deep the traversal should go (default is 1).
- **Direction:** Follow links `out` (outgoing), `in` (incoming), or `both`.
- **Filtering:** Optionally restrict the traversal to specific relationship types.

## Visualizing the Graph

The **VS Code extension** includes a dedicated Knowledge Graph viewer.

- **D3.js Powered:** Renders a force-directed graph of your memories.
- **Interactive:** Drag nodes, zoom in/out, and click to open specific memories.
- **Scope Coloring:** Nodes are automatically colored based on their scope.
- **Freshness Indicators:** Pinned nodes and the current root node are visually highlighted.

## Cascading Deletion
l0-memory maintains referential integrity. When a memory is deleted, all links incident to it (both incoming and outgoing) are automatically removed, preventing "dangling" edges in your graph.
