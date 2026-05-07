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

## Troubleshooting: macOS provenance gate

On macOS Sequoia (15) and Tahoe (16), unsigned Mach-O binaries spawned by a
signed app (Claude Desktop, Cursor, …) can be **silently terminated by the
kernel** within ~25 ms — no panic, no stderr, no log. Symptoms in the host
log: `Server started and connected successfully` followed almost immediately
by `Server transport closed unexpectedly`.

The fix is an ad-hoc code signature:

```sh
codesign --sign - --force --timestamp=none /path/to/ltm
```

Both `make build` and the release workflow now do this automatically for
darwin builds. If you build manually with `go build` and skip the Makefile,
you have to run `codesign` yourself before the binary will be allowed to
run as a subprocess of a signed host.

The pre-existing `com.apple.provenance` extended attribute on the binary is
benign once the signature is in place; it does *not* need to be stripped.
