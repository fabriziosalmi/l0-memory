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
    icon: 🔒
  - title: Model Context Protocol
    details: Native MCP support over stdio. Connect to Claude Code, Claude Desktop, Cursor, and any other MCP-compatible host.
    icon: 🔌
  - title: Knowledge Graph
    details: Typed, directional links between memories. Visualize your assistant's memory as a force-directed graph.
    icon: 🕸️
  - title: Scopes & Freshness
    details: Partition memories by scope (user, repo, desktop). Track staleness with verification and pinning.
    icon: ⏲️
  - title: VS Code Extension
    details: A professional TreeView UI with built-in search, filtering, and a D3.js based graph visualization.
    icon: 💻
  - title: Zero CGO
    details: Pure Go implementation. Cross-compiles easily to every major platform (Linux, macOS, Windows).
    icon: 🚀
---
