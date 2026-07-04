package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// loadServeToken returns the bearer token that the local REST server requires,
// plus a human-readable note about where it came from (for the startup banner).
//
// Precedence:
//  1. LTM_SERVE_TOKEN env — used as-is, never persisted.
//  2. <db-dir>/serve-token — reused if present (co-located with memories.db).
//  3. Otherwise a fresh random token is generated, written 0600, and returned.
//
// The token is what actually gates the API: 127.0.0.1 binding alone does not
// stop a malicious web page from calling the server, so every request (except
// GET /health) must present this token.
func loadServeToken() (token, source string, err error) {
	if t := strings.TrimSpace(os.Getenv("LTM_SERVE_TOKEN")); t != "" {
		return t, "LTM_SERVE_TOKEN env", nil
	}

	dbPath, err := defaultDBPath()
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(filepath.Dir(dbPath), "serve-token")

	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, path, nil
		}
	}

	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return "", "", err
	}
	return t, path, nil
}
