---
layout: home

hero:
  name: "l0-memory"
  text: "Long-term memory for AI assistants."
  tagline: SQLite-backed, local-first, knowledge-graph-driven memory for MCP hosts.
  actions:
    - theme: brand
      text: Get Started
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/fabriziosalmi/l0-memory

features:
  - title: Local-First & Private
    details: Backed by a single Go binary and a SQLite database. No network listeners, no embeddings, no external dependencies.
    icon:
      src: /icons/shield.svg
  - title: Model Context Protocol
    details: Native MCP support over stdio. Connect to Claude Code, Claude Desktop, Cursor, and any other MCP-compatible host.
    icon:
      src: /icons/plug.svg
  - title: Knowledge Graph
    details: Typed, directional links between memories. Visualize your assistant's memory as a force-directed graph.
    icon:
      src: /icons/network.svg
  - title: Scopes & Freshness
    details: Partition memories by scope (user, repo, desktop). Track staleness with verification and pinning.
    icon:
      src: /icons/history.svg
  - title: VS Code Extension
    details: A professional TreeView UI with built-in search, filtering, and a D3.js based graph visualization.
    icon:
      src: /icons/code.svg
  - title: Zero CGO
    details: Pure Go implementation. Cross-compiles easily to every major platform (Linux, macOS, Windows).
    icon:
      src: /icons/zap.svg
---
