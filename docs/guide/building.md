# Building from Source

This guide provides detailed instructions on how to build the **l0-memory** server and VS Code extension from source.

## Prerequisites

Ensure you have the following tools installed:
- **Go 1.22 or newer**
- **Node.js 20 or newer** (including `npm`)
- **Make** (standard on macOS and Linux)

## Using the Makefile

The project includes a comprehensive `Makefile` in the root directory that automates the most common build tasks.

### Build the Server
To compile the `ltm` binary for your current platform:
```sh
make build
```
The resulting binary will be located at `server/ltm`.

### Build the Extension
To compile the VS Code extension and package it as a `.vsix` file:
```sh
make vsix
```
This command performs several steps:
1. Compiles the Go server for multiple platforms (Linux, macOS, Windows).
2. Compiles the TypeScript extension code.
3. Bundles the cross-compiled binaries into the extension package.
4. Generates a `.vsix` file in the root directory.

## Manual Build Steps

If you prefer to build individual components manually:

### Server
```sh
cd server
go build -o ltm main.go
```

### Extension
```sh
cd extension
npm install
npm run compile
```

## Cross-Compilation

Because l0-memory uses a pure Go SQLite driver (no CGO), you can easily build for other platforms from your current machine.

```sh
# Example: Building for Linux ARM64 from macOS
GOOS=linux GOARCH=arm64 go build -o ltm-linux-arm64 ./server
```

## Testing

Always run the tests after building to ensure stability.

```sh
# Run server tests
make test

# Or manually in the server directory
cd server && go test -race ./...
```

::: tip CI/CD
The project uses GitHub Actions for continuous integration. You can refer to `.github/workflows/ci.yml` to see the exact steps used in the official build pipeline.
:::
