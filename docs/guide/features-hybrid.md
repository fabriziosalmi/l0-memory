# Hybrid Retrieval

By default, **l0-memory** searches with SQLite's **FTS5** full-text engine: fast, local, and dependency-free. FTS5 matches *literal tokens and their prefixes*, which is exactly what you want for code symbols, repo names, and exact phrases — but it has a blind spot. A query in one language can't find a memory written in another, and a paraphrase that shares no words with its target returns nothing.

Hybrid retrieval closes that gap by adding an **optional** semantic layer on top of FTS5.

::: tip Opt-in by design
The vector path is **off** until you configure an embedding endpoint. With it unset, search is pure FTS5 and nothing leaves your machine — identical to earlier versions.
:::

## How it works

When an embedding endpoint is configured:

1. **On save** — every `memory_save` embeds the value and stores the resulting vector in the same SQLite row, next to the text. This is best-effort: if the endpoint is unreachable, the memory is still saved durably; only the vector is skipped.
2. **On search** — `memory_search` runs the FTS5 query **and** a cosine-similarity search over the stored vectors, then blends the two rankings with **Reciprocal Rank Fusion (RRF, k=60)**.
3. **Tie-breaking** — pinned status only breaks ties on equal RRF score; it never overrides semantic relevance.

The result: a query like `idee per migliorare la memoria` can surface a semantically-perfect English memory it shares no token with, while exact-token matches keep ranking first.

## Enabling it

Point l0-memory at any **OpenAI-compatible** `/v1/embeddings` endpoint — [Ollama](https://ollama.com), LM Studio, vLLM, llmproxy, or OpenAI itself:

```sh
export LTM_EMBEDDING_URL="http://localhost:11434/v1"
export LTM_EMBEDDING_MODEL="nomic-embed-text"
```

Inside an MCP host, set these in the host's MCP server `env` block instead of the shell — see [Configuration](/reference/config).

### Backfill existing memories

Memories saved before you enabled embeddings have no vector yet. Backfill them once:

```sh
ltm reembed          # embed rows that are missing a vector
ltm reembed --force  # re-embed everything (e.g. after a model change)
```

Subsequent saves embed automatically. `reembed` collects per-row errors without aborting, so a transient endpoint hiccup never loses the rows already processed. It refuses to run when no endpoint is configured.

## Storage & safety

- The embedding is stored as a raw little-endian `float32` BLOB in the `embedding` column; `embedding_model` records which model produced it.
- A query vector whose dimensionality differs from a stored vector (for example after switching models without `--force`) silently skips that row instead of returning garbage — a model swap degrades gracefully rather than corrupting results.
- The schema upgrade is additive (`ALTER TABLE ADD COLUMN`): a v0.6+ binary reads older databases transparently, and an older binary keeps working against a newer database.

## Configuration reference

| Variable | Default | Description |
| :--- | :--- | :--- |
| `LTM_EMBEDDING_URL` | (empty) | OpenAI-compatible `/v1/embeddings` base URL. Empty disables the vector path. |
| `LTM_EMBEDDING_MODEL` | (empty) | Embedding model name sent to the endpoint. |
| `LTM_EMBED_DISABLE` | (empty) | Set to `1` to force the vector path off even when a URL is configured. |
| `LTM_EMBED_TIMEOUT` | `5s` | Per-request timeout as a Go duration (e.g. `3s`, `500ms`). |
