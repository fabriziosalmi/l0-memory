package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// memory URIs use an empty authority and put scope and key as path segments.
// Each segment is URL-encoded so colons and slashes inside scope or key are
// preserved unambiguously. Examples:
//   memory:///user/focus_areas
//   memory:///repo%3Al0-memory/notes
const memoryURIPrefix = "memory:///"

func makeMemoryURI(scope, key string) string {
	return memoryURIPrefix + url.PathEscape(scope) + "/" + url.PathEscape(key)
}

func parseMemoryURI(uri string) (string, string, error) {
	if !strings.HasPrefix(uri, memoryURIPrefix) {
		return "", "", fmt.Errorf("not a memory URI: %q", uri)
	}
	rest := uri[len(memoryURIPrefix):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed memory URI: %q (want memory:///<scope>/<key>)", uri)
	}
	scope, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("decode scope: %w", err)
	}
	key, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("decode key: %w", err)
	}
	if scope == "" || key == "" {
		return "", "", fmt.Errorf("memory URI missing scope or key: %q", uri)
	}
	return scope, key, nil
}

func mimeTypeOf(value string) string {
	if json.Valid([]byte(value)) {
		return "application/json"
	}
	return "text/plain"
}

func (s *mcpServer) handleResourcesList(req *rpcRequest) {
	pinned, err := s.store.ListPinned(s.ctx, "", 0)
	if err != nil {
		s.writeError(req.ID, -32603, "list pinned: "+err.Error())
		return
	}
	resources := make([]map[string]any, 0, len(pinned))
	for _, m := range pinned {
		display := m.Key
		if m.Scope != DefaultScope {
			display = m.Scope + ":" + m.Key
		}
		resources = append(resources, map[string]any{
			"uri":         makeMemoryURI(m.Scope, m.Key),
			"name":        display,
			"description": m.Tags,
			"mimeType":    mimeTypeOf(m.Value),
		})
	}
	s.writeResult(req.ID, map[string]any{"resources": resources})
}

func (s *mcpServer) handleResourcesRead(req *rpcRequest) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	scope, key, err := parseMemoryURI(p.URI)
	if err != nil {
		s.writeError(req.ID, -32602, err.Error())
		return
	}
	m, err := s.store.Get(s.ctx, scope, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.writeError(req.ID, -32002, "resource not found: "+p.URI)
			return
		}
		s.writeError(req.ID, -32603, err.Error())
		return
	}
	s.writeResult(req.ID, map[string]any{
		"contents": []map[string]any{{
			"uri":      p.URI,
			"mimeType": mimeTypeOf(m.Value),
			"text":     m.Value,
		}},
	})
}

// notifyResourcesListChanged sends a JSON-RPC notification (no id) telling the
// host that the pinned-resource set may have changed. Hosts that declared
// listChanged support refresh their local copy.
func (s *mcpServer) notifyResourcesListChanged() {
	s.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/resources/list_changed",
	})
}
