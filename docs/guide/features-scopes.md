# Scopes & Freshness

Managing the relevance and lifecycle of information is critical for an effective long-term memory. **l0-memory** provides robust mechanisms for partitioning data and tracking its freshness.

## Scopes

Every memory is uniquely identified by a `(scope, key)` pair. This partitioning allows the same key to exist in different contexts without collision.

### Conventional Scopes

- **`user`**: The default scope for global facts, personal preferences, and cross-project notes.
- **`repo:<name>`**: Context specific to a particular repository or project.
- **`desktop` / `host`**: Notes specific to the current machine.

::: tip
When using the MCP tools, many hosts will automatically attempt to use a `repo:` scope based on your current working directory.
:::

## Freshness Signals

Information changes over time. l0-memory tracks several signals to help the assistant (and you) determine how much to trust a particular memory.

### Pinning
A **pinned** memory is one that you want to keep at the forefront of the assistant's context.
- Surfaced first in `list` and `search` results.
- Exposed as **MCP Resources**, making them directly "visible" to the host without a search.
- **Verification:** Pinning a memory automatically sets its `verified_at` timestamp to the current time.

### Verification
The `verified_at` timestamp records when a memory was last confirmed as accurate.
- Use `memory_verify` to refresh this timestamp without changing the memory's content.
- **Staleness:** Compact views report `staleness_days`, allowing the assistant to flag potentially outdated information.

### Archiving & Superseding
Instead of deleting old information, you can **supersede** it.
- **`memory_supersede`**: Archives the old memory and creates a new one.
- **Relationship:** Automatically creates a `supersedes` link from the new memory to the old one.
- **Visibility:** Archived memories are hidden from default `list` and `search` results but remain available via `get` or graph traversal. This preserves historical context and audit trails.

## Freshness Workflow Example

1. **Save:** Create a memory about a project's dependency version.
2. **Verify:** A month later, confirm the version is still current.
3. **Supersede:** When the dependency is upgraded, supersede the old memory with the new version. The assistant can still see the old version in the history via the `supersedes` link.
