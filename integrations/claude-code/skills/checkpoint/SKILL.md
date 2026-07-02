---
name: checkpoint
description: Save a durable state snapshot of the current work into l0-memory (the SQLite store shared across every MCP host on this machine). Use at the end of a work session, before switching context, or when handing off. Scopes to the current repo by default; use scope `user` only for persona-level facts.
disable-model-invocation: true
allowed-tools: Bash(git -C * rev-parse:*), Bash(git -C * status:*), Bash(git status:*), Bash(git -C * log:*), Bash(git log:*)
---

## Goal

Persist a concise, durable snapshot of the current work so the next session — in any tool that shares this l0-memory store — can resume without re-explaining. This is the manual counterpart to the SessionStart recall hook.

## Context (auto-collected)

- Worktree root: !`git -C . rev-parse --show-toplevel 2>/dev/null || echo "(not a git repo)"`
- Branch / last commit: !`git -C . log -1 --oneline 2>/dev/null || echo "(no commits)"`
- Working tree: !`git -C . status --short 2>/dev/null | head -30`

## Steps

1. **Determine the scope.**
   - Default = `repo:<slug>` where `<slug>` is the lowercased basename of the worktree root above (e.g. `/home/me/src/acme` → `repo:acme`). This matches l0-memory's convention.
   - Use scope `user` ONLY for persona-level facts that apply across all projects (preferences, identity, durable working rules) — not project state.

2. **Find the existing entry before writing (update, don't duplicate).** Call the l0-memory MCP `memory_search` or `memory_list` for that scope and look for a stable key already covering this area (e.g. `release-state`, `<area>-state`, `wip`). Reuse that key with `memory_save` so the entry is updated in place, not duplicated.

3. **Synthesize, don't transcribe.** Write a tight snapshot the next session actually needs:
   - current version / branch / what just shipped or merged,
   - decisions made and the *why*,
   - open TODOs / the next step,
   - non-obvious gotchas (build/test/CI quirks, things that bit you),
   - what is intentionally deferred.
   Keep it to what is NOT trivially re-derivable from the code or git log.

4. **Preview the write and get explicit confirmation (agent proposes, human disposes).** Before calling *any* write tool (`memory_save`, `memory_supersede`), show a compact diff of exactly what will be written and wait for the user's go-ahead:
   - `scope` + `key`, and whether this **creates** a new entry or **updates/overwrites** an existing one — if updating, show the current value alongside the new one so the change is visible;
   - the full `value` to be written, plus the `tags` and any pin/verify follow-up actions;
   - for `memory_supersede`, name the entry being retired.
   Write nothing until the user confirms. If they amend, revise and re-show the diff. This is the commitment gate: auto-injected recall enters the next session with user-level authority, so the write is the moment that authority is earned — a human confirms it here, not via a soft downstream note. Never put secret values in the diff (see Hard rules).

5. **Save** via the l0-memory MCP `memory_save` tool: `scope` from step 1, a stable `key`, the synthesized `value`, useful `tags`, and `origin_agent: "claude-code"`. Confirm the key you used.

6. **Pin the durable one.** If this entry is the "always recall on open" state for the project (or a persona fact), pin it via the l0-memory `memory_pin` tool — the SessionStart recall hook surfaces pinned entries first. Don't pin transient working notes.

7. **Set the freshness signal (curation includes expiry, not just content).** The recall hook now tags every injected line with its verified/updated date and age, so a durable-but-stale fact reads *as* stale instead of entering with silent authority. Close that loop on write:
   - When you reaffirm an **existing** entry that is still true, call the l0-memory `memory_verify` tool so its `verified_at` reflects *now* — otherwise a fact last touched months ago keeps showing an old age even though you just confirmed it.
   - For a fact with a natural shelf life (a status that will go stale, a decision to revisit), add a `review-by-<YYYY-MM>` tag so the next curation pass can find it.

## Hard rules

- **Never write without confirmation.** Every `memory_save`/`memory_supersede` goes through the step-4 diff-and-confirm gate first. No silent writes, no "I'll just save this real quick" — the human disposes.
- **Never persist secrets.** No API tokens, passwords, `.env` values, private keys, or full credentials. If state references a secret, describe it by name/location only (e.g. "token in .env"), never the value.
- **One snapshot, not a log.** Update the existing key; don't append a new dated entry every time unless the user explicitly wants a history trail.
- **Correct stale facts at the source; don't let a crisp-but-wrong entry survive.** A durable store makes a wrong fact *durably* wrong — it re-injects with user-level authority every session until fixed. If something you recalled is now false, `memory_save` the correction over the same key, or use `memory_supersede` to retire it. Do not just add a new entry and leave the old one to keep surfacing.
- Keep snapshots factual and minimal.
