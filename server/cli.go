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

func runCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ltm <list|get|search|query|save|delete|path|version> [args...]")
	}

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
		limit := 200
		if len(rest) > 0 {
			limit, _ = strconv.Atoi(rest[0])
		}
		ms, err := store.List(ctx, limit)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, ms)
	case "get":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm get <key>")
		}
		m, err := store.Get(ctx, rest[0])
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return writeJSON(os.Stdout, map[string]any{"found": false})
			}
			return err
		}
		return writeJSON(os.Stdout, m)
	case "search":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm search <query> [limit]")
		}
		limit := 50
		if len(rest) > 1 {
			limit, _ = strconv.Atoi(rest[1])
		}
		// CLI returns full records to keep scripts working unchanged.
		ms, err := store.SearchExpanded(ctx, rest[0], limit)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, ms)
	case "save":
		// ltm save <key> <value> [tags]   — value can be "-" to read stdin
		if len(rest) < 2 {
			return fmt.Errorf("usage: ltm save <key> <value|-> [tags]")
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
		m, err := store.Save(ctx, key, value, tags)
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, m)
	case "delete":
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm delete <key>")
		}
		ok, err := store.Delete(ctx, rest[0])
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, map[string]any{"deleted": ok})
	case "query":
		// ltm query <key> [path]    — path defaults to "" (whole document).
		if len(rest) < 1 {
			return fmt.Errorf("usage: ltm query <key> [path]")
		}
		key := rest[0]
		path := ""
		if len(rest) > 1 {
			path = rest[1]
		}
		m, err := store.Get(ctx, key)
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
