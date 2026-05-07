package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Version is set at build time via -ldflags="-X main.Version=...".
// Default keeps something useful for `go run` and unversioned builds.
var Version = "dev"

func main() {
	args := os.Args[1:]

	// Top-level version flag works in any mode.
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v" || args[0] == "version") {
		fmt.Println(Version)
		return
	}

	// Default mode: MCP stdio server. Any subcommand triggers CLI mode.
	if len(args) == 0 || args[0] == "mcp" {
		store, err := OpenStore()
		if err != nil {
			fail(err)
		}
		defer store.Close()

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if err := runMCP(ctx, store, os.Stdin, os.Stdout); err != nil {
			fail(err)
		}
		return
	}

	if err := runCLI(args); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
