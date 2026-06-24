#!/usr/bin/env bash
# Install the l0-memory Claude Code integration:
#   - the SessionStart recall hook  -> ~/.claude/hooks/l0-recall.py
#   - the /checkpoint skill          -> ~/.claude/skills/checkpoint/
#   - registers the hook in ~/.claude/settings.json (idempotent)
#
# Override the config dir with CLAUDE_CONFIG_DIR. Re-running is safe.
set -euo pipefail

SRC="$(cd "$(dirname "$0")" && pwd)"
CLAUDE_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
HOOK_DEST="$CLAUDE_DIR/hooks/l0-recall.py"
SKILL_DEST="$CLAUDE_DIR/skills/checkpoint"

echo "l0-memory :: Claude Code integration installer"
echo "  config dir: $CLAUDE_DIR"

mkdir -p "$CLAUDE_DIR/hooks" "$SKILL_DEST"

install -m 0755 "$SRC/l0-recall.py" "$HOOK_DEST"
echo "  installed hook : $HOOK_DEST"

cp "$SRC/skills/checkpoint/SKILL.md" "$SKILL_DEST/SKILL.md"
echo "  installed skill: $SKILL_DEST/SKILL.md  (use /checkpoint)"

# Register the SessionStart hook in settings.json, but only if it is not already
# registered there or in settings.local.json (so a manual setup is not doubled).
SETTINGS="$CLAUDE_DIR/settings.json" HOOK="$HOOK_DEST" \
  LOCAL="$CLAUDE_DIR/settings.local.json" python3 - <<'PY'
import json, os

settings = os.environ["SETTINGS"]
local = os.environ["LOCAL"]
hook = os.environ["HOOK"]

def registered(path):
    try:
        d = json.load(open(path))
    except Exception:
        return False
    for entry in d.get("hooks", {}).get("SessionStart", []):
        for h in entry.get("hooks", []):
            if h.get("command", "").endswith("l0-recall.py"):
                return True
    return False

if registered(settings) or registered(local):
    print("  hook already registered — skipping settings edit")
    raise SystemExit(0)

try:
    d = json.load(open(settings))
except Exception:
    d = {}
ss = d.setdefault("hooks", {}).setdefault("SessionStart", [])
ss.append({
    "matcher": "startup|resume|clear",
    "hooks": [{"type": "command", "command": hook}],
})
if os.path.exists(settings):
    import shutil, time
    shutil.copy(settings, settings + ".bak." + str(int(os.path.getmtime(settings))))
json.dump(d, open(settings, "w"), indent=2)
print(f"  registered SessionStart hook in {settings}")
PY

echo
echo "Done. Open a NEW Claude Code session to pick it up."
echo "Verify the hook:  echo '{\"cwd\":\"'\"\$PWD\"'\"}' | $HOOK_DEST | python3 -m json.tool | head"
