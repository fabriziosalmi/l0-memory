package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func seedTriple(t *testing.T, s *Store) context.Context {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "caddy-waf", "Caddy WAF plugin", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "user", "caddy-mib", "Caddy MIB plugin", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "user", "tech:caddy", "Caddy server", ""); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestLinkCreatesEdge(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)

	l, err := s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	if err != nil {
		t.Fatal(err)
	}
	if l.ID == 0 || l.Rel != "depends_on" {
		t.Fatalf("unexpected link: %+v", l)
	}
}

func TestLinkIsIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	a, err := s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("re-linking the same triple should be a no-op (same id), got %d then %d", a.ID, b.ID)
	}
}

func TestLinkRequiresRel(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	if _, err := s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", ""); err == nil {
		t.Fatal("expected error for empty rel")
	}
}

func TestLinkRequiresExistingEndpoints(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	if _, err := s.Link(ctx, "user", "ghost", "user", "tech:caddy", "depends_on"); err == nil {
		t.Fatal("FK should refuse a missing source memory")
	}
}

func TestUnlinkDeletes(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	_, _ = s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	ok, err := s.Unlink(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	if err != nil || !ok {
		t.Fatalf("unlink: ok=%v err=%v", ok, err)
	}
	ok, _ = s.Unlink(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	if ok {
		t.Fatal("second unlink should return false")
	}
}

func TestDeleteMemoryCascadesLinks(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	_, _ = s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	_, _ = s.Link(ctx, "user", "caddy-mib", "user", "tech:caddy", "depends_on")

	if _, err := s.Delete(ctx, "user", "tech:caddy"); err != nil {
		t.Fatal(err)
	}
	links, err := s.Links(ctx, "user", "caddy-waf")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("ON DELETE CASCADE should drop incident links, got %+v", links)
	}
}

func TestNeighborsBothDirections(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	_, _ = s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	_, _ = s.Link(ctx, "user", "caddy-mib", "user", "tech:caddy", "depends_on")

	out, err := s.Neighbors(ctx, "user", "tech:caddy", "", DirIn)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(out))
	for i, n := range out {
		keys[i] = n.Memory.Key
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "caddy-mib" || keys[1] != "caddy-waf" {
		t.Errorf("incoming neighbors of tech:caddy should be both plugins, got %v", keys)
	}

	out2, err := s.Neighbors(ctx, "user", "caddy-waf", "", DirOut)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2) != 1 || out2[0].Memory.Key != "tech:caddy" {
		t.Errorf("outgoing of caddy-waf should be tech:caddy, got %+v", out2)
	}
}

func TestNeighborsRelFilter(t *testing.T) {
	s := newStore(t)
	ctx := seedTriple(t, s)
	_, _ = s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "depends_on")
	_, _ = s.Link(ctx, "user", "caddy-waf", "user", "tech:caddy", "see_also")

	dep, _ := s.Neighbors(ctx, "user", "caddy-waf", "depends_on", DirOut)
	if len(dep) != 1 || dep[0].Rel != "depends_on" {
		t.Errorf("rel filter should isolate depends_on; got %+v", dep)
	}
	any, _ := s.Neighbors(ctx, "user", "caddy-waf", "", DirOut)
	if len(any) != 2 {
		t.Errorf("no rel filter should yield both edges, got %d", len(any))
	}
}

func TestTraverseDepth(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c", "d"} {
		if _, err := s.Save(ctx, "user", k, "v", ""); err != nil {
			t.Fatal(err)
		}
	}
	// a -> b -> c -> d
	_, _ = s.Link(ctx, "user", "a", "user", "b", "next")
	_, _ = s.Link(ctx, "user", "b", "user", "c", "next")
	_, _ = s.Link(ctx, "user", "c", "user", "d", "next")

	depth1, err := s.Traverse(ctx, "user", "a", 1, "", DirOut)
	if err != nil {
		t.Fatal(err)
	}
	if len(depth1.Nodes) != 2 {
		t.Errorf("depth=1 from a should return {a,b}, got %d nodes", len(depth1.Nodes))
	}

	depth2, _ := s.Traverse(ctx, "user", "a", 2, "", DirOut)
	if len(depth2.Nodes) != 3 {
		t.Errorf("depth=2 should return {a,b,c}, got %d", len(depth2.Nodes))
	}

	depth9, _ := s.Traverse(ctx, "user", "a", 9, "", DirOut)
	if len(depth9.Nodes) != 4 {
		t.Errorf("deep traverse should reach all 4 nodes, got %d", len(depth9.Nodes))
	}
}

func TestTraverseHandlesCycles(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Save(ctx, "user", k, "v", ""); err != nil {
			t.Fatal(err)
		}
	}
	// a -> b -> c -> a (cycle)
	_, _ = s.Link(ctx, "user", "a", "user", "b", "next")
	_, _ = s.Link(ctx, "user", "b", "user", "c", "next")
	_, _ = s.Link(ctx, "user", "c", "user", "a", "next")

	view, err := s.Traverse(ctx, "user", "a", 10, "", DirOut)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 3 {
		t.Errorf("cycle should be visited once per node, got %d nodes", len(view.Nodes))
	}
}

// TestTraverseDedupsEdgesInBothDirection is the regression for the bug where
// `direction=both` emitted incident edges twice: once when visiting the
// `from` node (as DirOut) and again when visiting the `to` node (as DirIn,
// reconstructed back to the same (from, to, rel) triple). Verified against
// real production data 2026-05-09 — wishlist:bold_ideas → brainstorm_push_depth
// appeared twice in the edges array.
func TestTraverseDedupsEdgesInBothDirection(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Save(ctx, "user", k, "v", ""); err != nil {
			t.Fatal(err)
		}
	}
	// a -> b (derived_from); b -> c (refines).
	// At depth=2 from a with direction=both, BFS reaches b then c.
	// Without dedup, edge a→b would be emitted twice (once visiting a as
	// outgoing, once visiting b as incoming).
	if _, err := s.Link(ctx, "user", "a", "user", "b", "derived_from"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Link(ctx, "user", "b", "user", "c", "refines"); err != nil {
		t.Fatal(err)
	}

	view, err := s.Traverse(ctx, "user", "a", 2, "", DirBoth)
	if err != nil {
		t.Fatal(err)
	}

	// Count occurrences of each (from, to, rel) triple.
	counts := map[string]int{}
	for _, e := range view.Edges {
		k := e.From + "|" + e.To + "|" + e.Rel
		counts[k]++
	}
	for k, n := range counts {
		if n > 1 {
			t.Errorf("edge %s emitted %d times, want exactly 1; full edges=%+v", k, n, view.Edges)
		}
	}
	// Sanity: we still emit both edges, just once each.
	if len(view.Edges) != 2 {
		t.Errorf("expected exactly 2 distinct edges in subgraph, got %d: %+v", len(view.Edges), view.Edges)
	}
}

func TestTraverseCrossScope(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "tech:caddy", "Caddy", "")
	_, _ = s.Save(ctx, "repo:waf", "main", "WAF repo", "")
	_, _ = s.Link(ctx, "repo:waf", "main", "user", "tech:caddy", "uses")

	view, err := s.Traverse(ctx, "user", "tech:caddy", 1, "", DirIn)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 2 {
		t.Errorf("incoming traverse across scopes should reach repo:waf/main, got nodes %+v", view.Nodes)
	}
	scopes := map[string]bool{}
	for _, n := range view.Nodes {
		scopes[n.Scope] = true
	}
	if !scopes["repo:waf"] {
		t.Error("traverse should cross scope boundaries")
	}
}

func TestMCPMemoryTraverseRoundTrip(t *testing.T) {
	all := runRequestsAll(t, []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "tech:caddy", "value": "Caddy server"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
			"name":      "memory_save",
			"arguments": map[string]any{"key": "caddy-waf", "value": "Caddy WAF"},
		}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
			"name":      "memory_link",
			"arguments": map[string]any{"from_key": "caddy-waf", "to_key": "tech:caddy", "rel": "depends_on"},
		}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{
			"name":      "memory_traverse",
			"arguments": map[string]any{"key": "tech:caddy", "depth": 2, "direction": "in"},
		}},
	})
	// Pick the response with id=4.
	var view map[string]any
	for _, m := range all {
		if id, ok := m["id"].(float64); ok && id == 4 {
			text := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
			if err := json.NewDecoder(strings.NewReader(text)).Decode(&view); err != nil {
				t.Fatalf("parse traverse view: %v", err)
			}
			break
		}
	}
	if view == nil {
		t.Fatal("no response for id=4")
	}
	nodes := view["nodes"].([]any)
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes (root + caddy-waf), got %d", len(nodes))
	}
	edges := view["edges"].([]any)
	if len(edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(edges))
	}
}
