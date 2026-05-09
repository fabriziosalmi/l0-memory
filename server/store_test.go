package main

import (
	"context"
	"database/sql"
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

	m, err := s.Save(ctx, "user", "alpha", "first value", "tag1,tag2")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if m.Key != "alpha" || m.Value != "first value" || m.Tags != "tag1,tag2" {
		t.Fatalf("unexpected memory: %+v", m)
	}
	if m.CreatedAt == 0 || m.UpdatedAt == 0 {
		t.Fatalf("timestamps must be set: %+v", m)
	}

	got, err := s.Get(ctx, "user", "alpha")
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

	first, err := s.Save(ctx, "user", "k", "v1", "")
	if err != nil {
		t.Fatalf("save 1: %v", err)
	}
	second, err := s.Save(ctx, "user", "k", "v2", "tagged")
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
	if _, err := s.Save(context.Background(), "user", "  ", "v", ""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestGetNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Get(context.Background(), "user", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "del", "v", ""); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Delete(ctx, "user", "del")
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	ok, err = s.Delete(ctx, "user", "del")
	if err != nil || ok {
		t.Fatalf("second delete should report not deleted: ok=%v err=%v", ok, err)
	}
}

func TestListOrdersByUpdatedDesc(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Save(ctx, "user", k, "v", ""); err != nil {
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

	out, err := s.List(ctx, "", 0)
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
	out, err := s.List(context.Background(), "", 0)
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
	_, _ = s.Save(ctx, "user", "fruit_apple", "red and crunchy", "snack")
	_, _ = s.Save(ctx, "user", "fruit_banana", "yellow tropical", "snack,potassium")
	_, _ = s.Save(ctx, "user", "veg_carrot", "orange root", "")

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
		got, err := s.Search(ctx, "", tc.query, 0)
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
	_, _ = s.Save(ctx, "user", "k1", "100% percent", "")
	_, _ = s.Save(ctx, "user", "k2", "underscore_test", "")
	_, _ = s.Save(ctx, "user", "k3", "nothing relevant", "")

	// "%" must NOT act as a LIKE wildcard — only the literal "100% percent" should match.
	got, err := s.Search(ctx, "", "100%", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "k1" {
		t.Fatalf("expected only k1, got %+v", got)
	}

	// "_" must be literal.
	got, err = s.Search(ctx, "", "underscore_", 0)
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
	if _, err := s.Save(ctx, "user", "KEY", "MixedCase Value", "TagOne"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"key", "mixedcase", "tagone"} {
		got, err := s.Search(ctx, "", q, 0)
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
	_, _ = s.Save(ctx, "user", "k1", "kubernetes operator", "")
	_, _ = s.Save(ctx, "user", "k2", "kubelet daemon", "")
	_, _ = s.Save(ctx, "user", "k3", "completely unrelated", "")

	got, err := s.Search(ctx, "", "kube", 0)
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
	_, _ = s.Save(ctx, "user", "rss_llm", "Aggregate RSS feeds with LLM rewriting", "rss,llm")
	_, _ = s.Save(ctx, "user", "rss_only", "Aggregate RSS feeds, plain", "rss")
	_, _ = s.Save(ctx, "user", "llm_only", "LLM-powered code generation", "llm")

	got, err := s.Search(ctx, "", "rss llm", 0)
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
	_, _ = s.Save(ctx, "user", "repo:caddy-waf", "Caddy plugin", "caddy,waf")

	got, err := s.Search(ctx, "", "caddy", 0)
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
	if _, err := s.Save(ctx, "user", "k", "v", ""); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"", "   ", "\t\n"} {
		got, err := s.Search(ctx, "", q, 0)
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
	_, _ = s.Save(ctx, "user", "k", "100% reliable", "")

	// Plain special-char queries that would break a naive FTS5 expression
	// should either match via FTS phrase quoting or fall back to LIKE without
	// blowing up.
	for _, q := range []string{`100%`, `"hello`, `()`, `AND OR NOT`, `*`} {
		if _, err := s.Search(ctx, "", q, 0); err != nil {
			t.Errorf("query %q errored: %v", q, err)
		}
	}
}

func TestSearchRanksMostRelevantFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Both contain "caddy" but the second has it many times -> bm25 should
	// rank it higher.
	_, _ = s.Save(ctx, "user", "passing_mention", "Some other tool. Caddy is mentioned once.", "")
	_, _ = s.Save(ctx, "user", "caddy_focused", "caddy caddy caddy plugin caddy server caddy waf", "caddy")

	got, err := s.Search(ctx, "", "caddy", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0].Key != "caddy_focused" {
		t.Fatalf("expected 'caddy_focused' first, got %+v", keysOf(got))
	}
}

// TestSearchSortsPinnedFirst is the regression for the bug where pinned
// rule-shaped memories (the load-bearing user-curated ones) lost search
// ranking to longer dump-shaped memories with denser token matches.
// Verified live 2026-05-09 — query "brainstorm bold push past safe"
// returned wishlist:bold_ideas first while pinned feedback:brutal_honesty
// (conceptually adjacent) didn't surface at all. Pin signals user intent;
// memory_list already orders pinned-first, memory_search now matches.
func TestSearchSortsPinnedFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Two memories matching "alpha". The unpinned one has way denser
	// token matches and would win on raw BM25 alone.
	if _, err := s.Save(ctx, "user", "dump", "alpha alpha alpha alpha alpha alpha lorem ipsum", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "user", "rule", "alpha is the load-bearing constraint", ""); err != nil {
		t.Fatal(err)
	}
	// Pin only the rule-shaped one.
	if _, err := s.Pin(ctx, "user", "rule", true); err != nil {
		t.Fatal(err)
	}

	got, err := s.Search(ctx, "", "alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("expected 2 hits, got %d: %+v", len(got), keysOf(got))
	}
	if got[0].Key != "rule" {
		t.Fatalf("pinned memory must rank first regardless of bm25; got order %+v", keysOf(got))
	}
	if !got[0].Pinned {
		t.Errorf("first hit should carry pinned=true in compact view")
	}
	// Unpin and verify BM25 wins again — proves the boost is from pinned
	// state, not from anything else we accidentally changed.
	if _, err := s.Pin(ctx, "user", "rule", false); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Search(ctx, "", "alpha", 0)
	if len(got2) < 2 || got2[0].Key != "dump" {
		t.Fatalf("after unpin, dense-bm25 'dump' should win again; got %+v", keysOf(got2))
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
	if _, err := s.Save(ctx, "user", "before_index", "needle in the haystack", "demo"); err != nil {
		t.Fatal(err)
	}

	// Simulate a DB created before the FTS index existed by clearing it out.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memories_fts`); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Search(ctx, "", "needle", 0)
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
	got, err = s2.Search(ctx, "", "needle", 0)
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

	if _, err := s.Save(ctx, "user", "drift", "alpha bravo", ""); err != nil {
		t.Fatal(err)
	}

	// After update, only the new content should be searchable.
	if _, err := s.Save(ctx, "user", "drift", "charlie delta", ""); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, "", "alpha", 0); len(hits) != 0 {
		t.Errorf("post-update: 'alpha' should not match; got %+v", keysOf(hits))
	}
	if hits, _ := s.Search(ctx, "", "charlie", 0); len(hits) != 1 {
		t.Errorf("post-update: 'charlie' should match exactly once; got %+v", keysOf(hits))
	}

	// After delete, nothing should match.
	if _, err := s.Delete(ctx, "user", "drift"); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, "", "charlie", 0); len(hits) != 0 {
		t.Errorf("post-delete: 'charlie' should not match; got %+v", keysOf(hits))
	}
}

func TestSearchReturnsSnippetWithHighlight(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "doc", "the quick brown fox jumps over the lazy dog and continues running across the field", ""); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, "", "fox", 0)
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
	_, _ = s.Save(ctx, "user", "low", "caddy mentioned once.", "")
	_, _ = s.Save(ctx, "user", "high", "caddy caddy caddy caddy caddy in many places caddy caddy", "")

	hits, err := s.Search(ctx, "", "caddy", 0)
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
	if _, err := s.Save(ctx, "user", "k", "the value content", "tag"); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchExpanded(ctx, "", "value", 0)
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
	if _, err := s.Save(ctx, "user", "big", body, ""); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, "", "kubernetes", 0)
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

// --- Verify / Supersede / Origin / Archived (0.5 freshness) ---------------

func TestVerifyUpdatesTimestamp(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "fact", "stable", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "user", "fact")
	if got.VerifiedAt != 0 {
		t.Fatalf("new memories should start at verified_at=0, got %d", got.VerifiedAt)
	}
	m, err := s.Verify(ctx, "user", "fact")
	if err != nil {
		t.Fatal(err)
	}
	if m.VerifiedAt == 0 {
		t.Fatalf("verify should set verified_at, got %+v", m)
	}
}

func TestVerifyNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Verify(context.Background(), "user", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPinImpliesVerify(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "imp", "v", "")
	m, _ := s.Pin(ctx, "user", "imp", true)
	if m.VerifiedAt == 0 {
		t.Errorf("pinning should imply verify, got verified_at=0")
	}
}

func TestSaveWithOptionsRecordsOrigin(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	m, err := s.SaveWithOptions(ctx, "user", "k", "v", "", &SaveOptions{
		Origin:      "session abc",
		OriginAgent: "claude-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Origin != "claude-code: session abc" {
		t.Errorf("expected combined origin, got %q", m.Origin)
	}
}

func TestSaveOriginPreservedOnUpdateWhenEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.SaveWithOptions(ctx, "user", "k", "v1", "", &SaveOptions{Origin: "first"})
	_, _ = s.Save(ctx, "user", "k", "v2", "")
	m, _ := s.Get(ctx, "user", "k")
	if m.Origin != "first" {
		t.Errorf("origin should survive a no-origin update, got %q", m.Origin)
	}
}

func TestSupersedeArchivesOldAndCreatesLink(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "old_view", "Old preference: X", "")
	if _, err := s.Supersede(ctx, "user", "old_view", "new_view", "New preference: Y", ""); err != nil {
		t.Fatal(err)
	}
	old, _ := s.Get(ctx, "user", "old_view")
	if !old.Archived {
		t.Errorf("old key should be archived after supersede, got %+v", old)
	}
	newM, _ := s.Get(ctx, "user", "new_view")
	if newM.Archived {
		t.Errorf("new key should not be archived")
	}
	links, _ := s.Links(ctx, "user", "new_view")
	found := false
	for _, l := range links {
		if l.FromKey == "new_view" && l.ToKey == "old_view" && l.Rel == "supersedes" {
			found = true
		}
	}
	if !found {
		t.Errorf("supersedes link missing: %+v", links)
	}
}

func TestSupersedeRefusesIfNewExists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "a", "v", "")
	_, _ = s.Save(ctx, "user", "b", "v", "")
	if _, err := s.Supersede(ctx, "user", "a", "b", "x", ""); err == nil {
		t.Fatal("expected error: new_key already taken")
	}
}

func TestArchivedHiddenFromListAndSearchByDefault(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "old", "stale fact", "")
	_, _ = s.Supersede(ctx, "user", "old", "new", "fresh fact", "")

	items, _ := s.List(ctx, "", 0)
	for _, m := range items {
		if m.Key == "old" {
			t.Errorf("List should hide archived rows by default; got %v", m.Key)
		}
	}
	hits, _ := s.Search(ctx, "", "stale", 0)
	for _, h := range hits {
		if h.Key == "old" {
			t.Errorf("Search should hide archived rows; got %v", h.Key)
		}
	}
	// But ListIncludingArchived returns it.
	all, _ := s.ListIncludingArchived(ctx, "", 0)
	hasOld := false
	for _, m := range all {
		if m.Key == "old" {
			hasOld = true
		}
	}
	if !hasOld {
		t.Errorf("ListIncludingArchived should surface 'old'")
	}
}

func TestMigrationFromPre05Schema(t *testing.T) {
	dir := t.TempDir()
	dbpath := filepath.Join(dir, "pre05.db")

	// Build a v0.4-shape DB by hand: scope+pinned exist, but no archived /
	// origin / verified_at columns.
	old, err := sql.Open("sqlite", dbpath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL DEFAULT 'user',
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(scope, key)
		);
		INSERT INTO memories (scope, key, value, tags, pinned, created_at, updated_at)
		VALUES ('user', 'legacy', 'pre-0.5', 'migrate', 0, 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	s, err := openStoreAt(dbpath)
	if err != nil {
		t.Fatalf("open after migration: %v", err)
	}
	defer s.Close()
	got, err := s.Get(context.Background(), "user", "legacy")
	if err != nil {
		t.Fatalf("legacy entry missing after migration: %v", err)
	}
	if got.VerifiedAt != 0 || got.Archived || got.Origin != "" {
		t.Errorf("0.5 columns should default to zero/empty: %+v", got)
	}
}

// --- Rename ----------------------------------------------------------------

func TestRenameMovesKeyAndUpdatesLinks(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "old_key", "v", "tag")
	_, _ = s.Save(ctx, "user", "other", "v", "")
	_, _ = s.Link(ctx, "user", "old_key", "user", "other", "see_also")
	_, _ = s.Link(ctx, "user", "other", "user", "old_key", "depends_on")

	m, err := s.Rename(ctx, "user", "old_key", "new_key")
	if err != nil {
		t.Fatal(err)
	}
	if m.Key != "new_key" {
		t.Fatalf("got key %q, want new_key", m.Key)
	}
	// Old key gone.
	if _, err := s.Get(ctx, "user", "old_key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old_key should be gone, got %v", err)
	}
	// Links rewritten on both sides.
	links, _ := s.Links(ctx, "user", "new_key")
	if len(links) != 2 {
		t.Fatalf("expected 2 links incident to new_key, got %d", len(links))
	}
	keys := []string{}
	for _, l := range links {
		if l.FromKey == "new_key" {
			keys = append(keys, "out:"+l.ToKey)
		}
		if l.ToKey == "new_key" {
			keys = append(keys, "in:"+l.FromKey)
		}
	}
	if len(keys) != 2 {
		t.Errorf("links not pointing at new_key: %+v", links)
	}
}

func TestRenameSameKeyIsNoOp(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "k", "v", "")
	m, err := s.Rename(ctx, "user", "k", "k")
	if err != nil || m.Key != "k" {
		t.Fatalf("same-key rename should be a no-op: %+v err=%v", m, err)
	}
}

func TestRenameNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Rename(context.Background(), "user", "ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRenameRefusesCollision(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "a", "v", "")
	_, _ = s.Save(ctx, "user", "b", "v", "")
	if _, err := s.Rename(ctx, "user", "a", "b"); err == nil {
		t.Fatal("expected collision error")
	}
}

func TestRenameKeepsFTSConsistent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "stable_key", "needle in the haystack", "")
	if _, err := s.Rename(ctx, "user", "stable_key", "renamed"); err != nil {
		t.Fatal(err)
	}
	hits, _ := s.Search(ctx, "", "needle", 0)
	if len(hits) != 1 || hits[0].Key != "renamed" {
		t.Errorf("FTS should follow rename; got %+v", keysOf(hits))
	}
}

// --- Scope + pinning -------------------------------------------------------

func TestSaveSameKeyInDifferentScopes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	a, err := s.Save(ctx, "user", "focus", "user-level note", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save(ctx, "repo:l0-memory", "focus", "repo-level note", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("two different rows expected, got duplicate id=%d", a.ID)
	}

	got, err := s.Get(ctx, "user", "focus")
	if err != nil || got.Value != "user-level note" {
		t.Fatalf("user/focus mismatch: %+v err=%v", got, err)
	}
	got, err = s.Get(ctx, "repo:l0-memory", "focus")
	if err != nil || got.Value != "repo-level note" {
		t.Fatalf("repo/focus mismatch: %+v err=%v", got, err)
	}
}

func TestEmptyScopeDefaultsToUserOnSaveGetDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "", "implicit", "v", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "", "implicit")
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "user" {
		t.Errorf("expected scope=user, got %q", got.Scope)
	}
	ok, err := s.Delete(ctx, "", "implicit")
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
}

func TestListAllScopesByDefault(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "a", "v", "")
	_, _ = s.Save(ctx, "repo:x", "b", "v", "")
	_, _ = s.Save(ctx, "repo:y", "c", "v", "")

	all, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 rows across all scopes, got %d", len(all))
	}

	repoX, err := s.List(ctx, "repo:x", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(repoX) != 1 || repoX[0].Key != "b" {
		t.Fatalf("scope filter failed: %+v", repoX)
	}
}

func TestSearchScopeFilter(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "k1", "kubernetes operator", "")
	_, _ = s.Save(ctx, "repo:cluster", "k2", "kubernetes scheduler", "")

	all, _ := s.Search(ctx, "", "kubernetes", 0)
	if len(all) != 2 {
		t.Errorf("all-scopes should match both, got %d", len(all))
	}
	repo, _ := s.Search(ctx, "repo:cluster", "kubernetes", 0)
	if len(repo) != 1 || repo[0].Key != "k2" {
		t.Errorf("scope filter on search failed: %+v", repo)
	}
}

func TestPinSetsAndUnsetsTheFlag(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "important", "fact", ""); err != nil {
		t.Fatal(err)
	}
	m, err := s.Pin(ctx, "user", "important", true)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Pinned {
		t.Error("Pinned should be true after pin")
	}
	m, err = s.Pin(ctx, "user", "important", false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Pinned {
		t.Error("Pinned should be false after unpin")
	}
}

func TestPinNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Pin(context.Background(), "user", "ghost", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListSortsPinnedFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "old_pinned", "old", "")
	_, _ = s.Save(ctx, "user", "fresh_unpinned", "fresh", "")
	if _, err := s.Pin(ctx, "user", "old_pinned", true); err != nil {
		t.Fatal(err)
	}
	// Bump the unpinned one's updated_at to be the freshest.
	_, _ = s.db.ExecContext(ctx, `UPDATE memories SET updated_at=999999999999 WHERE key='fresh_unpinned'`)

	out, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Key != "old_pinned" {
		t.Errorf("pinned should come first even when unpinned is fresher; got %v", keysOfMemories(out))
	}
}

func TestListPinnedReturnsOnlyPinned(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "a", "v", "")
	_, _ = s.Save(ctx, "user", "b", "v", "")
	_, _ = s.Save(ctx, "repo:x", "c", "v", "")
	_, _ = s.Pin(ctx, "user", "a", true)
	_, _ = s.Pin(ctx, "repo:x", "c", true)

	all, _ := s.ListPinned(ctx, "", 0)
	if len(all) != 2 {
		t.Errorf("expected 2 pinned across scopes, got %d", len(all))
	}
	user, _ := s.ListPinned(ctx, "user", 0)
	if len(user) != 1 || user[0].Key != "a" {
		t.Errorf("user-scoped pinned wrong: %+v", user)
	}
}

func TestMigrationFromV0_1Schema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	// Hand-build a v0.1.x DB: memories without scope/pinned, key UNIQUE.
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			value TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO memories (key, value, tags, created_at, updated_at)
		VALUES ('legacy', 'pre-0.2 entry', 'legacy', 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	// Open via openStoreAt — migration should run transparently.
	s, err := openStoreAt(path)
	if err != nil {
		t.Fatalf("open after migration: %v", err)
	}
	defer s.Close()

	got, err := s.Get(context.Background(), "user", "legacy")
	if err != nil {
		t.Fatalf("legacy entry missing after migration: %v", err)
	}
	if got.Scope != "user" || got.Value != "pre-0.2 entry" || got.Pinned {
		t.Errorf("migrated row has unexpected fields: %+v", got)
	}

	// FTS should be working too: search across all scopes finds the legacy row.
	hits, err := s.Search(context.Background(), "", "pre-0.2", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Key != "legacy" {
		t.Errorf("FTS over migrated row failed: %+v", hits)
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
