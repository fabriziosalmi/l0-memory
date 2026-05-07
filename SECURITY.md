# Security Policy

## Supported Versions

l0-memory is pre-1.0. Only the latest commit on `main` receives security fixes.

## Reporting a Vulnerability

Please do **not** open a public GitHub issue for security problems.

Email: **fabrizio.salmi@gmail.com** with:
- A clear description of the issue
- Steps to reproduce
- Affected version / commit hash
- Any proposed mitigation

You can expect an acknowledgement within 7 days. Coordinated disclosure is appreciated; once a fix is released, credit will be given in the changelog unless you prefer to remain anonymous.

## Threat model

- The `ltm` binary stores memories in a local SQLite file at `~/.long-term-memory/memories.db` (or `$LTM_DB`).
- The MCP server reads/writes via stdio — no network listener, no authentication beyond OS file permissions.
- The VSCode extension shells out to `ltm` and is bound to the user's local environment.
- Memory contents are stored in plaintext. Do not store secrets, credentials, or PII you wouldn't put in a normal text file in your home directory.
