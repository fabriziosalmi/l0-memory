package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryURIRoundTrip(t *testing.T) {
	cases := []struct {
		scope, key string
	}{
		{"user", "focus_areas"},
		{"repo:l0-memory", "notes"},
		{"weird scope/with slashes", "key:with:colons"},
		{"plain", "k"},
	}
	for _, c := range cases {
		uri := makeMemoryURI(c.scope, c.key)
		gotScope, gotKey, err := parseMemoryURI(uri)
		if err != nil {
			t.Errorf("uri %q: parse failed: %v", uri, err)
			continue
		}
		if gotScope != c.scope || gotKey != c.key {
			t.Errorf("round-trip: %q -> got (%q, %q), want (%q, %q)", uri, gotScope, gotKey, c.scope, c.key)
		}
	}
}

func TestParseMemoryURIRejectsMalformed(t *testing.T) {
	bad := []string{
		"http://example/foo",
		"memory://just-host",
		"memory:///only-scope",
		"memory:///",
		"memory:///%ZZ/x",
	}
	for _, b := range bad {
		if _, _, err := parseMemoryURI(b); err == nil {
			t.Errorf("should reject %q", b)
		}
	}
}

func TestMCPResourcesListReturnsOnlyPinned(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "alpha", "value": "plain text", "tags": "demo"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "beta", "value": `{"a":1}`, "tags": "json"},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_pin",
			"arguments": map[string]any{"key": "beta", "pinned": true},
		}},
		{"jsonrpc": "2.0", "id": 4, "method": "resources/list"},
	})

	last := resps[len(resps)-1]
	result := last["result"].(map[string]any)
	resources := result["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("expected exactly 1 pinned resource, got %d (%+v)", len(resources), resources)
	}
	r := resources[0].(map[string]any)
	if r["uri"] != "memory:///user/beta" {
		t.Errorf("uri=%v", r["uri"])
	}
	if r["mimeType"] != "application/json" {
		t.Errorf("mimeType=%v (json value should yield application/json)", r["mimeType"])
	}
	if r["name"] != "beta" {
		t.Errorf("name=%v", r["name"])
	}
}

func TestMCPResourcesListIncludesScopeInName(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"scope": "repo:l0-memory", "key": "notes", "value": "x"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_pin",
			"arguments": map[string]any{"scope": "repo:l0-memory", "key": "notes", "pinned": true},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "resources/list"},
	})
	last := resps[len(resps)-1]
	resources := last["result"].(map[string]any)["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	r := resources[0].(map[string]any)
	// Non-default scope shows up in `name` so the host UI can disambiguate.
	if r["name"] != "repo:l0-memory:notes" {
		t.Errorf("name should disambiguate scope, got %v", r["name"])
	}
	// `:` is a valid path-segment character per RFC 3986 so PathEscape leaves
	// it alone; we only need encoding for separators like `/` to survive.
	if !strings.HasPrefix(r["uri"].(string), "memory:///repo:l0-memory/notes") {
		t.Errorf("uri shape unexpected, got %v", r["uri"])
	}
}

func TestMCPResourcesReadReturnsContent(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "doc", "value": "hello world"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_pin",
			"arguments": map[string]any{"key": "doc", "pinned": true},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "resources/read", "params": map[string]any{
			"uri": "memory:///user/doc",
		}},
	})
	last := resps[len(resps)-1]
	contents := last["result"].(map[string]any)["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	c := contents[0].(map[string]any)
	if c["text"] != "hello world" {
		t.Errorf("text=%v", c["text"])
	}
	if c["mimeType"] != "text/plain" {
		t.Errorf("mimeType=%v", c["mimeType"])
	}
}

func TestMCPResourcesReadMissingURIErrors(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "resources/read", "params": map[string]any{
			"uri": "memory:///user/ghost",
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "resources/read", "params": map[string]any{
			"uri": "https://example.com/foo",
		}},
	})
	if _, ok := resps[0]["error"]; !ok {
		t.Errorf("missing resource should be a JSON-RPC error: %+v", resps[0])
	}
	if _, ok := resps[1]["error"]; !ok {
		t.Errorf("non-memory URI should be a JSON-RPC error: %+v", resps[1])
	}
}

func TestMCPPinEmitsListChangedNotification(t *testing.T) {
	all := runRequestsAll(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "n", "value": "v"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_pin",
			"arguments": map[string]any{"key": "n", "pinned": true},
		}},
	})

	var notif map[string]any
	for _, r := range all {
		if _, hasID := r["id"]; hasID {
			continue
		}
		if r["method"] == "notifications/resources/list_changed" {
			notif = r
			break
		}
	}
	if notif == nil {
		got, _ := json.Marshal(all)
		t.Fatalf("expected a notifications/resources/list_changed after memory_pin; got %s", got)
	}
}
