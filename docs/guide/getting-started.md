# Getting Started

Learn how to install and set up **l0-memory** on your machine.

## Prerequisites

- **Go 1.22+** (if building from source)
- **Node.js 20+** (for the VS Code extension)
- **SQLite** (usually pre-installed on most systems)

## Installation

### From a Release

The easiest way to get started is by downloading the pre-built binaries from the [Releases page](https://github.com/fabriziosalmi/l0-memory/releases).

1.  **Server (`ltm`):** Download the archive for your OS/Architecture, extract it, and place the `ltm` binary in your `PATH`.
2.  **VS Code Extension:** Download the `.vsix` file and install it using:
    ```sh
    code --install-extension l0-memory-<version>.vsix
    ```

::: info macOS Users
The macOS binaries are ad-hoc codesigned to satisfy the Sequoia/Tahoe provenance gate. See [Security](/guide/architecture#security) for details.
:::

### From Source

If you prefer to build from source, ensure you have the required toolchains installed.

```sh
# Clone the repository
git clone https://github.com/fabriziosalmi/l0-memory.git
cd l0-memory

# Build the server binary
make build

# Package the VS Code extension (optional)
make vsix
```

## Quick Start

Once installed, you can verify the installation by running:

```sh
ltm version
```

The default database will be created at `~/.long-term-memory/memories.db`. You can override this by setting the `LTM_DB` environment variable.

### Your first memory

Save a cross-project note:

```sh
ltm save tech:caddy "Caddy is a powerful web server with automatic HTTPS." user
```

Retrieve it:

```sh
ltm get tech:caddy
```

List your memories:

```sh
ltm list
```
