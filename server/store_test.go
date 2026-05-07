package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := openStoreAt(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSaveAndGet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	m, err := s.Save(ctx, "alpha", "first value", "tag1,tag2")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if m.Key != "alpha" || m.Value != "first value" || m.Tags != "tag1,tag2" {
		t.Fatalf("unexpected memory: %+v", m)
	}
	if m.CreatedAt == 0 || m.UpdatedAt == 0 {
		t.Fatalf("timestamps must be set: %+v", m)
	}

	got, err := s.Get(ctx, "alpha")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != m.ID || got.Value != m.Value {
		t.Fatalf("get returned different row: %+v vs %+v", got, m)
	}
}

func TestSaveUpsertsByKey(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first, err := s.Save(ctx, "k", "v1", "")
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	second, err := s.Save(ctx, "k", "v2", "tagged")
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("upsert should reuse id: %d vs %d", first.ID, second.ID)
	}
	if second.Value != "v2" || second.Tags != "tagged" {
		t.Fatalf("upsert did not update fields: %+v", second)
	}
	n, _ := s.Count(ctx)
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
}

func TestSaveRequiresKey(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save(context.Background(), "  ", "v", ""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestGetNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "del", "v", ""); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Delete(ctx, "del")
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	ok, err = s.Delete(ctx, "del")
	if err != nil || ok {
		t.Fatalf("second delete should report not deleted: ok=%v err=%v", ok, err)
	}
}

func TestListOrdersByUpdatedDesc(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Save(ctx, k, "v", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Force distinct, monotonic updated_at so the test is independent of the
	// host clock resolution: a < b < c, then bump 'a' past everything.
	for i, k := range []string{"a", "b", "c"} {
		if _, err := s.db.ExecContext(ctx, `UPDATE memories SET updated_at = ? WHERE key = ?`, 1000+int64(i), k); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE memories SET updated_at = ? WHERE key = ?`, int64(9999), "a"); err != nil {
		t.Fatal(err)
	}

	out, err := s.List(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("len=%d want 3", len(out))
	}
	if out[0].Key != "a" || out[1].Key != "c" || out[2].Key != "b" {
		t.Fatalf("unexpected order: %v %v %v", out[0].Key, out[1].Key, out[2].Key)
	}
}

func TestListEmptyReturnsEmptySlice(t *testing.T) {
	s := newStore(t)
	out, err := s.List(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("List must return non-nil empty slice for JSON marshalling")
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %d", len(out))
	}
}

func TestSearchMatchesKeyValueAndTags(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "fruit_apple", "red and crunchy", "snack")
	_, _ = s.Save(ctx, "fruit_banana", "yellow tropical", "snack,potassium")
	_, _ = s.Save(ctx, "veg_carrot", "orange root", "")

	tests := []struct {
		query string
		want  int
	}{
		{"fruit", 2},
		{"yellow", 1},
		{"snack", 2},
		{"missing", 0},
	}
	for _, tc := range tests {
		got, err := s.Search(ctx, tc.query, 0)
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		if len(got) != tc.want {
			t.Errorf("search %q: got %d, want %d", tc.query, len(got), tc.want)
		}
	}
}

func TestSearchEscapesLikeWildcards(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "k1", "100% percent", "")
	_, _ = s.Save(ctx, "k2", "underscore_test", "")
	_, _ = s.Save(ctx, "k3", "nothing relevant", "")

	// "%" must NOT act as a LIKE wildcard — only the literal "100% percent" should match.
	got, err := s.Search(ctx, "100%", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "k1" {
		t.Fatalf("expected only k1, got %+v", got)
	}

	// "_" must be literal.
	got, err = s.Search(ctx, "underscore_", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "k2" {
		t.Fatalf("expected only k2, got %+v", got)
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "KEY", "MixedCase Value", "TagOne"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"key", "mixedcase", "tagone"} {
		got, err := s.Search(ctx, q, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Errorf("query %q: expected 1, got %d", q, len(got))
		}
	}
}

// --- FTS5-specific behaviour ------------------------------------------------

func TestSearchPrefixMatch(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "k1", "kubernetes operator", "")
	_, _ = s.Save(ctx, "k2", "kubelet daemon", "")
	_, _ = s.Save(ctx, "k3", "completely unrelated", "")

	got, err := s.Search(ctx, "kube", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("prefix 'kube' should match both kubelet and kubernetes, got %d", len(got))
	}
}

func TestSearchImplicitAnd(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "rss_llm", "Aggregate RSS feeds with LLM rewriting", "rss,llm")
	_, _ = s.Save(ctx, "rss_only", "Aggregate RSS feeds, plain", "rss")
	_, _ = s.Save(ctx, "llm_only", "LLM-powered code generation", "llm")

	got, err := s.Search(ctx, "rss llm", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "rss_llm" {
		t.Fatalf("expected only 'rss_llm', got %+v", keysOf(got))
	}
}

func TestSearchTokenSplitsOnPunctuation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// unicode61 tokenizer treats '_' '-' '.' as separators, so each piece is
	// independently searchable.
	_, _ = s.Save(ctx, "repo:caddy-waf", "Caddy plugin", "caddy,waf")

	got, err := s.Search(ctx, "caddy", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 hit for 'caddy', got %d", len(got))
	}
}

func TestSearchEmptyQueryReturnsEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "k", "v", ""); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"", "   ", "\t\n"} {
		got, err := s.Search(ctx, q, 0)
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if len(got) != 0 {
			t.Errorf("query %q: expected 0, got %d", q, len(got))
		}
	}
}

func TestSearchSpecialCharactersDoNotError(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "k", "100% reliable", "")

	// Plain special-char queries that would break a naive FTS5 expression
	// should either match via FTS phrase quoting or fall back to LIKE without
	// blowing up.
	for _, q := range []string{`100%`, `"hello`, `()`, `AND OR NOT`, `*`} {
		if _, err := s.Search(ctx, q, 0); err != nil {
			t.Errorf("query %q errored: %v", q, err)
		}
	}
}

func TestSearchRanksMostRelevantFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Both contain "caddy" but the second has it many times -> bm25 should
	// rank it higher.
	_, _ = s.Save(ctx, "passing_mention", "Some other tool. Caddy is mentioned once.", "")
	_, _ = s.Save(ctx, "caddy_focused", "caddy caddy caddy plugin caddy server caddy waf", "caddy")

	got, err := s.Search(ctx, "caddy", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0].Key != "caddy_focused" {
		t.Fatalf("expected 'caddy_focused' first, got %+v", keysOf(got))
	}
}

func TestFTSBackfillsRowsMissingFromIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "migrate.db")

	s, err := openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Save(ctx, "before_index", "needle in the haystack", "demo"); err != nil {
		t.Fatal(err)
	}

	// Simulate a DB created before the FTS index existed by clearing it out.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memories_fts`); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Search(ctx, "needle", 0)
	if len(got) != 0 {
		t.Fatalf("precondition: search should miss after wiping fts, got %d", len(got))
	}
	_ = s.Close()

	// Re-open: backfillFTS must repopulate the index.
	s2, err := openStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err = s2.Search(ctx, "needle", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "before_index" {
		t.Fatalf("backfill should have restored the index; got %+v", keysOf(got))
	}
}

func TestFTSStaysInSyncOnUpdateAndDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Save(ctx, "drift", "alpha bravo", ""); err != nil {
		t.Fatal(err)
	}

	// After update, only the new content should be searchable.
	if _, err := s.Save(ctx, "drift", "charlie delta", ""); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, "alpha", 0); len(hits) != 0 {
		t.Errorf("post-update: 'alpha' should not match; got %+v", keysOf(hits))
	}
	if hits, _ := s.Search(ctx, "charlie", 0); len(hits) != 1 {
		t.Errorf("post-update: 'charlie' should match exactly once; got %+v", keysOf(hits))
	}

	// After delete, nothing should match.
	if _, err := s.Delete(ctx, "drift"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, "charlie", 0); len(hits) != 0 {
		t.Errorf("post-delete: 'charlie' should not match; got %+v", keysOf(hits))
	}
}

func TestSearchReturnsSnippetWithHighlight(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "doc", "the quick brown fox jumps over the lazy dog and continues running across the field", ""); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, "fox", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if !strings.Contains(h.Snippet, "<<fox>>") {
		t.Errorf("snippet should highlight 'fox' with <<...>>, got %q", h.Snippet)
	}
	if h.Score <= 0 {
		t.Errorf("score should be positive (BM25 sign-flipped), got %v", h.Score)
	}
	if h.SizeBytes == 0 {
		t.Errorf("size_bytes should be set")
	}
}

func TestSearchHitsAreOrderedByScoreDesc(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "low", "caddy mentioned once.", "")
	_, _ = s.Save(ctx, "high", "caddy caddy caddy caddy caddy in many places caddy caddy", "")

	hits, err := s.Search(ctx, "caddy", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Key != "high" {
		t.Errorf("highest-scoring hit should be 'high', got %s", hits[0].Key)
	}
	if hits[0].Score < hits[1].Score {
		t.Errorf("scores should be in descending order, got %v then %v", hits[0].Score, hits[1].Score)
	}
}

func TestSearchExpandedReturnsFullRecords(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "k", "the value content", "tag"); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchExpanded(ctx, "value", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Value != "the value content" {
		t.Fatalf("expanded search should return full Memory.Value: %+v", hits)
	}
}

func TestSearchHitDoesNotCarryFullValue(t *testing.T) {
	// Defense-in-depth: a SearchHit must NEVER include the value field.
	// (We rely on this for the compact-by-default token reduction.)
	s := newStore(t)
	ctx := context.Background()
	body := strings.Repeat("kubernetes ", 100)
	if _, err := s.Save(ctx, "big", body, ""); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, "kubernetes", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit")
	}
	// The struct doesn't have a Value field — we encode/decode via JSON to
	// catch accidental marshalling regressions too.
	b, _ := json.Marshal(hits[0])
	if strings.Contains(string(b), "value") || strings.Contains(string(b), `"`+body+`"`) {
		t.Errorf("SearchHit JSON should not contain the full value: %s", b)
	}
	if hits[0].SizeBytes != len(body) {
		t.Errorf("size_bytes should match body length")
	}
}

func keysOf(hits []SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Key
	}
	return out
}

func keysOfMemories(ms []Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Key
	}
	return out
}
