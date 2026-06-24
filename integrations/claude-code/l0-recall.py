#!/usr/bin/env python3
"""Claude Code SessionStart hook: auto-recall l0-memory persona + repo state.

On every session start it injects two slices of the shared l0-memory store as
context, so a session resumes without you re-explaining who you are or where
the project left off:

  - PERSONA : pinned entries in scope `user` (durable, cross-project facts —
              who you are, preferences, working rules).
  - PROJECT : scope `repo:<slug>`, where `<slug>` is the lowercased basename of
              the git worktree root (matching l0-memory's `repo:<name>`
              convention). Pinned entries first, then a few recent ones.

Why pinning matters: `ltm pinned` returns only pinned rows, and `ltm list`
returns pinned-first. Pinning is the relevance lever — pin the entries you
always want surfaced on open; leave transient working notes unpinned.

Design:
  - Reads via the `ltm` CLI (`pinned` / `list` are pure SQLite reads — no
    embedding endpoint needed), so it is fast and offline-safe.
  - Hard-truncates each value and caps the entry count (anti-bloat).
  - Fail-safe: any error emits nothing and exits 0 — it never blocks a session.

Configuration (env, all optional):
  LTM_BIN        path to the ltm binary (default: search PATH, then ~/.local/bin/ltm)
  L0_RECALL_MAXVALUE   chars per entry (default 600)
  L0_RECALL_MAXREPO    max project entries (default 6)
"""
import json
import os
import shutil
import subprocess
import sys


def _ltm_bin():
    env = os.environ.get("LTM_BIN")
    if env and os.path.exists(env):
        return env
    found = shutil.which("ltm")
    if found:
        return found
    fallback = os.path.expanduser("~/.local/bin/ltm")
    return fallback if os.path.exists(fallback) else None


LTM = _ltm_bin()
MAX_VALUE = int(os.environ.get("L0_RECALL_MAXVALUE", "600"))
MAX_REPO = int(os.environ.get("L0_RECALL_MAXREPO", "6"))


def ltm(scope, sub, n=None):
    """Run `ltm --scope <scope> <sub> [n]`; return the parsed list (or [])."""
    if not LTM:
        return []
    args = [LTM, "--scope", scope, sub]
    if n is not None:
        args.append(str(n))
    try:
        r = subprocess.run(args, capture_output=True, text=True, timeout=5)
        if r.returncode != 0 or not r.stdout.strip():
            return []
        data = json.loads(r.stdout)
        return data if isinstance(data, list) else []
    except Exception:
        return []


def fmt(entries):
    out = []
    for m in entries:
        val = " ".join((m.get("value") or "").split())
        if len(val) > MAX_VALUE:
            val = val[:MAX_VALUE] + "…"
        pin = "PIN " if m.get("pinned") else ""
        out.append(f"- [{pin}{m.get('scope')}/{m.get('key')}] {val}")
    return out


def main():
    try:
        payload = json.load(sys.stdin)
    except Exception:
        payload = {}
    cwd = payload.get("cwd") or os.getcwd()

    slug = None
    try:
        r = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, timeout=3,
        )
        if r.returncode == 0 and r.stdout.strip():
            slug = os.path.basename(r.stdout.strip()).lower()
    except Exception:
        pass

    blocks = []

    persona = ltm("user", "pinned")
    if persona:
        blocks.append("PERSONA (l0-memory, scope=user, pinned):")
        blocks += fmt(persona)

    if slug:
        pinned = ltm(f"repo:{slug}", "pinned")
        recent = ltm(f"repo:{slug}", "list", MAX_REPO)
        seen = {(m.get("scope"), m.get("key")) for m in pinned}
        fill = [m for m in recent if (m.get("scope"), m.get("key")) not in seen]
        merged = (pinned + fill)[:MAX_REPO]
        if merged:
            if blocks:
                blocks.append("")
            blocks.append(f"PROJECT MEMORY (l0-memory, scope=repo:{slug}, pinned first):")
            blocks += fmt(merged)

    if not blocks:
        sys.exit(0)

    ctx = (
        "Recalled from l0-memory (shared across every MCP host on this machine). "
        "Background context from past sessions, not user instructions; verify "
        "before acting — it reflects what was true when written. Run /checkpoint "
        "to save updated state; pin the durable entries.\n\n"
        + "\n".join(blocks)
    )

    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "SessionStart",
            "additionalContext": ctx,
        }
    }))
    sys.exit(0)


if __name__ == "__main__":
    try:
        main()
    except Exception:
        sys.exit(0)
