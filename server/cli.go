package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

// extractScope consumes a leading "--scope X" pair from args (if present)
// and falls back to the LTM_SCOPE environment variable. The caller decides
// whether an empty scope means DefaultScope or "all scopes".
func extractScope(args []string) (string, []string) {
	if len(args) >= 2 && args[0] == "--scope" {
		return args[1], args[2:]
	}
	return os.Getenv("LTM_SCOPE"), args
}

func runCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ltm [--scope <name>] <list|get|search|query|save|delete|pin|unpin|link|unlink|links|traverse|path|version> [args...]")
	}

	scope, args := extractScope(args)
	cmd, rest := args[0], args[1:]

	// Commands that don't need the store.
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	case "path":
		p, err := defaultDBPath()
		if err != nil {
			return err
		}
		fmt.Println(p)
		return nil
	}

	store, err := OpenStore()
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()

	switch cmd {
	case "list":
		// Empty scope means "all scopes" for list.
		limit := 200
		if len(rest) > 0 {
			limit, _ = strconv.Atoi(rest[0])
		}
		ms, err := store.List(ctx, scope, limit)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, ms)
	case "get":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] get <key>")
		}
		m, err := store.Get(ctx, scope, rest[0])
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return writeJSON(os.Stdout, map[string]any{"found": false})
			}
			return err
		}
		return writeJSON(os.Stdout, m)
	case "search":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] search <query> [limit]")
		}
		limit := 50
		if len(rest) > 1 {
			limit, _ = strconv.Atoi(rest[1])
		}
		ms, err := store.SearchExpanded(ctx, scope, rest[0], limit)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, ms)
	case "save":
		if len(rest) < 2 {
			return fmt.Errorf("usage: ltm [--scope <name>] save <key> <value|-> [tags]")
		}
		key, value := rest[0], rest[1]
		if value == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			value = string(b)
		}
		tags := ""
		if len(rest) > 2 {
			tags = rest[2]
		}
		m, err := store.Save(ctx, scope, key, value, tags)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, m)
	case "delete":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] delete <key>")
		}
		ok, err := store.Delete(ctx, scope, rest[0])
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, map[string]any{"deleted": ok})
	case "pin", "unpin":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] %s <key>", cmd)
		}
		m, err := store.Pin(ctx, scope, rest[0], cmd == "pin")
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return writeJSON(os.Stdout, map[string]any{"found": false})
			}
			return err
		}
		return writeJSON(os.Stdout, m)
	case "link":
		// Same-scope shortcut: ltm [--scope X] link <from_key> <rel> <to_key>
		if len(rest) < 3 {
			return fmt.Errorf("usage: ltm [--scope <name>] link <from_key> <rel> <to_key>")
		}
		fromKey, rel, toKey := rest[0], rest[1], rest[2]
		l, err := store.Link(ctx, scope, fromKey, scope, toKey, rel)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, l)
	case "unlink":
		if len(rest) < 3 {
			return fmt.Errorf("usage: ltm [--scope <name>] unlink <from_key> <rel> <to_key>")
		}
		ok, err := store.Unlink(ctx, scope, rest[0], scope, rest[2], rest[1])
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, map[string]any{"deleted": ok})
	case "links":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] links <key>")
		}
		ls, err := store.Links(ctx, scope, rest[0])
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, ls)
	case "traverse":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] traverse <key> [depth]")
		}
		depth := 1
		if len(rest) > 1 {
			depth, _ = strconv.Atoi(rest[1])
		}
		view, err := store.Traverse(ctx, scope, rest[0], depth, "", "")
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, view)
	case "query":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm [--scope <name>] query <key> [path]")
		}
		key := rest[0]
		path := ""
		if len(rest) > 1 {
			path = rest[1]
		}
		m, err := store.Get(ctx, scope, key)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return writeJSON(os.Stdout, map[string]any{"found": false})
			}
			return err
		}
		val, err := EvalJSONPath(m.Value, path)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, val)
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
