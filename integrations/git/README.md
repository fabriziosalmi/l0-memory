# l0-memory Git Hook Integration (Offline)

This directory contains a pre-built local Git hook that automatically captures and saves your commit messages into your local `l0-memory` store on every commit. This allows your AI assistant (e.g. Claude, Cline, Cursor) to instantly query recent commit context when you ask questions about the project history.

## How it works

1. When you run `git commit`, the `post-commit` script is triggered.
2. It reads the last commit's SHA and message.
3. It resolves the repository name and saves the details into `l0-memory` under the scope `repo:<repository_name>` with tags `git,commit`.
4. The save runs completely offline and bypasses auto-conflict-resolution to avoid messing up historical logs.

## Installation

To install the Git hook in your current git repository, run the installer script:

```bash
chmod +x integrations/git/install-hooks.sh
./integrations/git/install-hooks.sh
```

If you want to install it on a different git repository, copy `integrations/git/install-hooks.sh` and `integrations/git/post-commit` into that repository's directory, and run the script there.
