package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// runRequestsAll pipes a sequence of JSON-RPC requests through runMCP and
// parses every per-line message — including server-emitted notifications,
// which look like requests (no id, method set).
func runRequestsAll(t *testing.T, requests []map[string]any) []map[string]any {
	t.Helper()
	store, err := openStoreAt(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	var in bytes.Buffer
	for _, r := range requests {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		in.Write(b)
		in.WriteByte('\n')
	}
	var out bytes.Buffer
	if err := runMCP(context.Background(), store, &in, &out); err != nil {
		t.Fatalf("runMCP: %v", err)
	}

	var msgs []map[string]any
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal response %q: %v", line, err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// runRequests is the most common helper: it returns only the request/response
// pairs, filtering out server-emitted notifications. Tests that want to
// observe notifications use runRequestsAll directly.
func runRequests(t *testing.T, requests []map[string]any) []map[string]any {
	t.Helper()
	all := runRequestsAll(t, requests)
	out := make([]map[string]any, 0, len(all))
	for _, m := range all {
		if _, hasID := m["id"]; hasID {
			out = append(out, m)
		}
	}
	return out
}

func TestMCPInitializeAndToolsList(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
	})
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}

	init := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion=%v want %s", init["protocolVersion"], protocolVersion)
	}
	server := init["serverInfo"].(map[string]any)
	if server["name"] != "l0-memory" {
		t.Errorf("serverInfo.name=%v", server["name"])
	}

	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	got := map[string]bool{}
	for _, tt := range tools {
		got[tt.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"memory_save", "memory_get", "memory_search", "memory_list", "memory_delete"} {
		if !got[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestMCPSaveGetDeleteRoundTrip(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "x", "value": "hello world", "tags": "demo"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_get",
			"arguments": map[string]any{"key": "x"},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_delete",
			"arguments": map[string]any{"key": "x"},
		}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{
			"name":      "memory_get",
			"arguments": map[string]any{"key": "x"},
		}},
	})
	if len(resps) != 4 {
		t.Fatalf("expected 4 responses, got %d", len(resps))
	}

	// Helper: extract the JSON payload returned in the first content[0].text.
	extract := func(i int) map[string]any {
		t.Helper()
		result := resps[i]["result"].(map[string]any)
		if isErr, _ := result["isError"].(bool); isErr {
			t.Fatalf("response %d unexpected error: %v", i, result)
		}
		text := result["content"].([]any)[0].(map[string]any)["text"].(string)
		var v map[string]any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("parse content %d: %v (%s)", i, err, text)
		}
		return v
	}

	saved := extract(0)
	if saved["key"] != "x" || saved["value"] != "hello world" || saved["tags"] != "demo" {
		t.Errorf("save returned %+v", saved)
	}

	got := extract(1)
	if got["key"] != "x" {
		t.Errorf("get returned %+v", got)
	}

	deleted := extract(2)
	if deleted["deleted"] != true {
		t.Errorf("delete returned %+v", deleted)
	}

	missing := extract(3)
	if missing["found"] != false {
		t.Errorf("get-after-delete should report found=false, got %+v", missing)
	}
}

func TestMCPMissingRequiredFieldsError(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"value": "no key"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_search",
			"arguments": map[string]any{},
		}},
	})
	for i, r := range resps {
		result := r["result"].(map[string]any)
		isErr, _ := result["isError"].(bool)
		if !isErr {
			t.Errorf("response %d should be a tool error, got %+v", i, result)
		}
	}
}

func TestMCPUnknownMethodReturnsRPCError(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "does/not/exist"},
	})
	if _, ok := resps[0]["error"]; !ok {
		t.Fatalf("expected JSON-RPC error, got %+v", resps[0])
	}
}

func TestMCPMemorySearchCompactByDefault(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "hits", "value": strings.Repeat("kubernetes orchestration ", 100), "tags": "k8s"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_search",
			"arguments": map[string]any{"query": "kubernetes"},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_search",
			"arguments": map[string]any{"query": "kubernetes", "expand": true},
		}},
	})

	// Compact response: array of SearchHit, no `value`, has snippet+score.
	compactText := resps[1]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var compact []map[string]any
	if err := json.Unmarshal([]byte(compactText), &compact); err != nil {
		t.Fatalf("parse compact: %v (%s)", err, compactText)
	}
	if len(compact) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(compact))
	}
	if _, has := compact[0]["value"]; has {
		t.Errorf("compact search hit must not include 'value'")
	}
	snip, _ := compact[0]["snippet"].(string)
	if !strings.Contains(snip, "<<kubernetes>>") {
		t.Errorf("snippet should highlight match, got %q", snip)
	}
	if score, _ := compact[0]["score"].(float64); score <= 0 {
		t.Errorf("score should be positive, got %v", score)
	}

	// expand:true returns the full record, including value.
	expandText := resps[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var expanded []map[string]any
	if err := json.Unmarshal([]byte(expandText), &expanded); err != nil {
		t.Fatalf("parse expanded: %v (%s)", err, expandText)
	}
	if len(expanded) != 1 || expanded[0]["value"] == nil {
		t.Fatalf("expanded search should include 'value', got %+v", expanded)
	}
}

func TestMCPMemoryGetCompactByDefault(t *testing.T) {
	// Realistic case: a multi-KB JSON blob where the compact view should be
	// dramatically smaller than the body. (Compact has overhead, so it only
	// pays off above ~500 bytes of payload — that's the regime we care about.)
	repos := strings.Repeat(`{"n":"name","s":1,"u":"https://example.com/repo"},`, 50)
	body := `{"stats":{"a":1},"repos":[` + strings.TrimRight(repos, ",") + `]}`
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "g", "value": body, "tags": "demo"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_get",
			"arguments": map[string]any{"key": "g"},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_get",
			"arguments": map[string]any{"key": "g", "expand": true},
		}},
	})
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(resps))
	}

	// Default response: compact, no `value`, has `schema`, has `hint`.
	compactText := resps[1]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var compact map[string]any
	if err := json.Unmarshal([]byte(compactText), &compact); err != nil {
		t.Fatalf("parse compact: %v (%s)", err, compactText)
	}
	if compact["compact"] != true {
		t.Errorf("default get should be compact: %+v", compact)
	}
	if _, has := compact["value"]; has {
		t.Errorf("compact get must not return value: %+v", compact)
	}
	if compact["is_json"] != true {
		t.Errorf("compact view should detect JSON: %+v", compact)
	}
	if _, has := compact["schema"]; !has {
		t.Errorf("compact view should include schema for JSON: %+v", compact)
	}

	// Compact payload should be meaningfully smaller than the body once the
	// payload is non-trivial.
	if len(compactText) >= len(body)/2 {
		t.Errorf("compact payload (%d B) should be << body (%d B)",
			len(compactText), len(body))
	}

	// expand:true returns the full record including `value`.
	expandText := resps[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var expand map[string]any
	if err := json.Unmarshal([]byte(expandText), &expand); err != nil {
		t.Fatalf("parse expand: %v (%s)", err, expandText)
	}
	if expand["value"] != body {
		t.Errorf("expanded get should return full body, got %v", expand["value"])
	}
	if _, has := expand["compact"]; has {
		t.Errorf("expanded get should not be marked compact")
	}
}

func TestMCPMemoryPinRoundTrip(t *testing.T) {
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "to_pin", "value": "important", "tags": ""},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_pin",
			"arguments": map[string]any{"key": "to_pin", "pinned": true},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_get",
			"arguments": map[string]any{"key": "to_pin"},
		}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{
			"name":      "memory_pin",
			"arguments": map[string]any{"key": "ghost", "pinned": true},
		}},
	})

	pinText := resps[1]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var pinned map[string]any
	if err := json.Unmarshal([]byte(pinText), &pinned); err != nil {
		t.Fatalf("parse pin response: %v (%s)", err, pinText)
	}
	if pinned["pinned"] != true {
		t.Errorf("memory_pin response should report pinned=true, got %+v", pinned)
	}

	// memory_get should reflect the pin in its compact view.
	getText := resps[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var got map[string]any
	if err := json.Unmarshal([]byte(getText), &got); err != nil {
		t.Fatalf("parse get: %v (%s)", err, getText)
	}
	if got["pinned"] != true {
		t.Errorf("memory_get compact view should reflect pinned=true, got %+v", got)
	}

	// Pinning a missing key returns {found:false}, not an error.
	missingText := resps[3]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var missing map[string]any
	_ = json.Unmarshal([]byte(missingText), &missing)
	if missing["found"] != false {
		t.Errorf("pin on missing key should be {found:false}, got %+v", missing)
	}
}

func TestMCPMemoryQueryRoundTrip(t *testing.T) {
	graph := `{"stats":{"repos":2},"repos":[{"n":"a","s":10},{"n":"b","s":20}]}`
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "g", "value": graph, "tags": "graph"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_query",
			"arguments": map[string]any{"key": "g", "path": "/stats/repos"},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_query",
			"arguments": map[string]any{"key": "g", "path": "/repos/*/n"},
		}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{
			"name":      "memory_query",
			"arguments": map[string]any{"key": "g", "path": "/missing"},
		}},
		{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{
			"name":      "memory_query",
			"arguments": map[string]any{"key": "ghost", "path": "/anything"},
		}},
	})
	if len(resps) != 5 {
		t.Fatalf("expected 5 responses, got %d", len(resps))
	}

	extract := func(i int) (map[string]any, bool) {
		t.Helper()
		result := resps[i]["result"].(map[string]any)
		isErr, _ := result["isError"].(bool)
		text := result["content"].([]any)[0].(map[string]any)["text"].(string)
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("response %d: %v (%s)", i, err, text)
		}
		mp, _ := v.(map[string]any)
		return mp, isErr
	}

	// /stats/repos -> 2
	if r := resps[1]["result"].(map[string]any); r["isError"] == true {
		t.Fatalf("query #2 should succeed, got %+v", r)
	}
	stats := resps[1]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if stats != "2" {
		t.Errorf("expected '2', got %q", stats)
	}

	// /repos/*/n -> ["a","b"]
	names := resps[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var got []string
	if err := json.Unmarshal([]byte(names), &got); err != nil {
		t.Fatalf("parse names: %v (%s)", err, names)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("unexpected names: %v", got)
	}

	// /missing on existing key -> tool error
	if !resps[3]["result"].(map[string]any)["isError"].(bool) {
		t.Errorf("missing path should be a tool error, got %+v", resps[3])
	}

	// any path on missing key -> {found:false}, not an error
	missing, isErr := extract(4)
	if isErr {
		t.Errorf("missing key should NOT be a tool error: %+v", resps[4])
	}
	if missing["found"] != false {
		t.Errorf("missing key should report found=false, got %+v", missing)
	}
}

func TestMCPNotificationsHaveNoResponse(t *testing.T) {
	// A request without an id is a JSON-RPC notification; the server must not respond.
	resps := runRequests(t, []map[string]any{
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 1, "method": "ping"},
	})
	if len(resps) != 1 {
		t.Fatalf("expected exactly 1 response (for ping), got %d: %+v", len(resps), resps)
	}
	if resps[0]["id"].(float64) != 1 {
		t.Errorf("expected response id=1, got %v", resps[0]["id"])
	}
}
