# Contributing

Thank you for your interest in contributing to **l0-memory**! This project is designed to be lightweight and focused—we value contributions that maintain this philosophy.

## Repository Layout

- **`server/`**: The Go source code for the `ltm` binary (MCP server and CLI).
- **`extension/`**: The TypeScript source code for the VS Code extension.
- **`docs/`**: This documentation site (VitePress).

## Development Workflow

### Server (Go)
The server requires Go 1.22+.
```sh
cd server
go build -o ltm .
./ltm list
```
We use `modernc.org/sqlite` to avoid CGO dependencies, ensuring easy cross-compilation.

### Extension (TypeScript)
The extension requires Node.js 20+.
```sh
cd extension
npm install
npm run compile
```
To test, open the `extension/` folder in VS Code and press `F5` to launch an Extension Development Host.

## Pull Request Guidelines

1. **Issue First:** For non-trivial changes, please open an issue first to discuss the approach.
2. **Focused PRs:** Keep your pull requests small and focused on a single logical change.
3. **Run Tests:** Ensure all tests pass before submitting.
   - `make test` runs the Go test suite.
   - `npm run compile` in the extension folder checks for TypeScript errors.
4. **Style:**
   - Go code should be formatted with `gofmt`.
   - TypeScript code should follow the project's `strict` configuration.

## Coding Principles

- **Minimal Dependencies:** Avoid adding new libraries unless there is a compelling reason.
- **Zero CGO:** Always use the pure Go SQLite driver.
- **Local-First:** Never add features that require network access or cloud services.
- **Stability:** Ensure that database migrations (if any) are handled safely.

## Bug Reports

Please use the GitHub Issues tracker to report bugs. Include your OS, Go/Node versions, and clear steps to reproduce the issue.
