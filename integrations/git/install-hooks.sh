#!/bin/sh
set -e

# Make sure we are in a git repository
GIT_DIR=$(git rev-parse --git-dir 2>/dev/null)
if [ -z "$GIT_DIR" ]; then
    echo "Error: Not a git repository. Run this from within a git repo." >&2
    exit 1
fi

HOOK_DIR="$GIT_DIR/hooks"
mkdir -p "$HOOK_DIR"

# Locate the post-commit template relative to this script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TEMPLATE="$SCRIPT_DIR/post-commit"

if [ ! -f "$TEMPLATE" ]; then
    echo "Error: post-commit script template not found at $TEMPLATE" >&2
    exit 1
fi

cp "$TEMPLATE" "$HOOK_DIR/post-commit"
chmod +x "$HOOK_DIR/post-commit"

echo "→ Successfully installed l0-memory post-commit Git hook into $HOOK_DIR/post-commit"
