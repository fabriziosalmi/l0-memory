package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runDoctor prints a one-shot health check of the whole l0-memory setup —
// binary, store, local REST server, embeddings, and the Claude Code recall
// hook — so a user (or a fresh-machine install) can see at a glance what is
// wired up and what still needs attention. Never returns an error for a failed
// check: the report itself is the output. It only errors on I/O it cannot do.
func runDoctor(ctx context.Context, store *Store) error {
	var warn int
	ok := func(label, detail string) { fmt.Printf("  ✓ %-22s %s\n", label, detail) }
	bad := func(label, detail string) { warn++; fmt.Printf("  ✗ %-22s %s\n", label, detail) }
	meh := func(label, detail string) { warn++; fmt.Printf("  ⚠ %-22s %s\n", label, detail) }

	fmt.Println("l0-memory doctor")
	fmt.Println()

	// --- binary ---
	fmt.Println("binary")
	exe, _ := os.Executable()
	if exe == "" {
		exe = "(unknown path)"
	}
	ok("version", fmt.Sprintf("%s  (%s)", Version, exe))

	// --- store ---
	fmt.Println("store")
	dbPath, err := defaultDBPath()
	if err != nil {
		bad("db path", err.Error())
	} else {
		n, err := store.Count(ctx)
		if err != nil {
			bad("db", fmt.Sprintf("%s — %v", dbPath, err))
		} else {
			ok("db", fmt.Sprintf("%s  (%d memories)", dbPath, n))
		}
	}

	// --- embeddings (optional) ---
	fmt.Println("embeddings (optional)")
	if url := os.Getenv("LTM_EMBEDDING_URL"); url == "" {
		ok("hybrid retrieval", "off (LTM_EMBEDDING_URL unset) — FTS5 keyword search only")
	} else {
		model := os.Getenv("LTM_EMBEDDING_MODEL")
		if reachable(url, 2*time.Second) {
			ok("embedding endpoint", fmt.Sprintf("%s (model %q) — reachable", url, model))
		} else {
			meh("embedding endpoint", fmt.Sprintf("%s configured but NOT reachable", url))
		}
	}

	// --- local REST server (web clipper) ---
	fmt.Println("REST server (web clipper)")
	if reachable("http://127.0.0.1:8080/health", 1*time.Second) {
		ok("serve", "running on http://127.0.0.1:8080")
	} else {
		meh("serve", "not running — start with `ltm serve` to use the web clipper")
	}
	if dbPath != "" {
		tokenPath := filepath.Join(filepath.Dir(dbPath), "serve-token")
		if _, err := os.Stat(tokenPath); err == nil {
			ok("serve token", tokenPath)
		} else if os.Getenv("LTM_SERVE_TOKEN") != "" {
			ok("serve token", "(from LTM_SERVE_TOKEN env)")
		} else {
			ok("serve token", "not yet generated — created on first `ltm serve`")
		}
	}

	// --- Claude Code integration (optional) ---
	fmt.Println("Claude Code integration (optional)")
	checkClaudeHook(ok, meh)

	fmt.Println()
	if warn == 0 {
		fmt.Println("all good ✓")
	} else {
		fmt.Printf("%d item(s) need attention (see ✗/⚠ above)\n", warn)
	}
	return nil
}

// reachable does a short-timeout GET and reports whether it got any HTTP
// response (any status counts as "something is listening").
func reachable(url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// checkClaudeHook best-effort inspects ~/.claude/settings.json for the
// SessionStart recall hook and the installed hook script.
func checkClaudeHook(ok, meh func(string, string)) {
	home, err := os.UserHomeDir()
	if err != nil {
		meh("recall hook", "cannot resolve home dir")
		return
	}
	cfgDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".claude")
	}

	registered := false
	for _, f := range []string{"settings.json", "settings.local.json"} {
		b, err := os.ReadFile(filepath.Join(cfgDir, f))
		if err == nil && strings.Contains(string(b), "l0-recall") {
			registered = true
			break
		}
	}
	if registered {
		ok("recall hook", "registered in settings.json")
	} else {
		meh("recall hook", "not registered — run `make install-claude` to enable auto-recall")
	}

	hookPath := filepath.Join(cfgDir, "hooks", "l0-recall.py")
	if _, err := os.Stat(hookPath); err == nil {
		ok("recall hook script", hookPath)
	} else {
		meh("recall hook script", "not installed — run `make install-claude`")
	}
}
