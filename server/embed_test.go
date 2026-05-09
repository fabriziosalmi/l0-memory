package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// makeFakeEmbedServer returns an httptest.Server that mimics LM Studio /
// Ollama / OpenAI /v1/embeddings. The supplied generator turns the request
// `input` into a deterministic float32 vector so tests can assert exact
// roundtrips. handlerCalls is incremented atomically to let tests assert
// idempotency and skip-already-embedded behaviour.
func makeFakeEmbedServer(t *testing.T, gen func(input string) []float32, handlerCalls *int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handlerCalls != nil {
			atomic.AddInt64(handlerCalls, 1)
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		vec := gen(req.Input)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(embedResponse{
			Model: req.Model,
			Data:  []embedResponseItem{{Embedding: vec, Index: 0, Object: "embedding"}},
		})
	}))
}

// fakeVec turns a string into a deterministic 4-dim vector. Cheap enough
// for the tests but distinct per input so we can assert "this row got
// embedded with input X".
func fakeVec(input string) []float32 {
	out := make([]float32, 4)
	for i, r := range input {
		out[i%4] += float32(r) * 0.001
	}
	return out
}

func TestEncodeDecodeEmbeddingRoundTrip(t *testing.T) {
	cases := [][]float32{
		nil,
		{},
		{0},
		{1.5, -2.25, 0, 1e-7, math.Pi, math.SmallestNonzeroFloat32, math.MaxFloat32},
	}
	for _, in := range cases {
		blob := encodeEmbedding(in)
		// Empty/nil should round-trip through nil; we don't care about the
		// exact intermediate shape (encodeEmbedding returns nil for nil and
		// a 0-byte slice for an empty non-nil — both decode to nil).
		out, err := decodeEmbedding(blob)
		if err != nil {
			t.Fatalf("decode err for %v: %v", in, err)
		}
		if len(in) == 0 {
			if len(out) != 0 {
				t.Errorf("empty input should decode to empty, got %v", out)
			}
			continue
		}
		if len(out) != len(in) {
			t.Fatalf("length mismatch: in=%d out=%d", len(in), len(out))
		}
		for i := range in {
			// math.Float32bits round-trips exactly, so == is correct here
			// (even for NaN we'd want bit-equality, but we don't put NaN in
			// the test set since the model never produces it).
			if out[i] != in[i] {
				t.Errorf("mismatch at %d: in=%v out=%v", i, in[i], out[i])
			}
		}
	}
}

func TestDecodeEmbeddingRejectsMalformed(t *testing.T) {
	if _, err := decodeEmbedding([]byte{1, 2, 3}); err == nil {
		t.Error("3-byte payload should error (not multiple of 4)")
	}
	if _, err := decodeEmbedding([]byte{1, 2, 3, 4, 5}); err == nil {
		t.Error("5-byte payload should error")
	}
}

func TestEmbedClientDisabled(t *testing.T) {
	c := NewEmbedClient("", "any-model", 0)
	if !c.Disabled() {
		t.Fatal("empty URL should yield Disabled()")
	}
	if _, err := c.Embed(context.Background(), "anything"); err != ErrEmbedDisabled {
		t.Fatalf("Embed on disabled client should return ErrEmbedDisabled, got %v", err)
	}
}

func TestEmbedClientFromEnvHonoursDisableFlag(t *testing.T) {
	t.Setenv("LTM_EMBEDDING_URL", "http://example.invalid")
	t.Setenv("LTM_EMBED_DISABLE", "1")
	c := NewEmbedClientFromEnv()
	if !c.Disabled() {
		t.Error("LTM_EMBED_DISABLE=1 should disable the client even with URL set")
	}
}

func TestEmbedClientHappyPath(t *testing.T) {
	srv := makeFakeEmbedServer(t, fakeVec, nil)
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)

	got, err := c.Embed(context.Background(), "ciao mondo")
	if err != nil {
		t.Fatal(err)
	}
	want := fakeVec("ciao mondo")
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("dim %d: got=%v want=%v", i, got[i], want[i])
		}
	}
}

func TestEmbedClientEmptyInputErrors(t *testing.T) {
	srv := makeFakeEmbedServer(t, fakeVec, nil)
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)
	if _, err := c.Embed(context.Background(), "   "); err == nil {
		t.Error("empty input should error before HTTP call")
	}
}

func TestEmbedClientHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)
	_, err := c.Embed(context.Background(), "ciao")
	if err == nil {
		t.Fatal("500 should produce an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status: %v", err)
	}
}

func TestEmbedClientEmptyResponseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(embedResponse{})
	}))
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)
	_, err := c.Embed(context.Background(), "ciao")
	if err == nil {
		t.Fatal("empty data array should error")
	}
}

func TestEmbedClientTimeoutHonoured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []embedResponseItem{{Embedding: []float32{1, 2, 3}}},
		})
	}))
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 50*time.Millisecond)
	if _, err := c.Embed(context.Background(), "ciao"); err == nil {
		t.Error("slow server should trip the per-request timeout")
	}
}

func TestSchemaV06AddsEmbeddingColumns(t *testing.T) {
	s := newStore(t)
	cols, err := columnSet(s.db, "memories")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"embedding", "embedding_model"} {
		if !cols[want] {
			t.Errorf("missing column %q on fresh DB; have %+v", want, cols)
		}
	}
}

func TestSetEmbeddingRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "alpha", "value", ""); err != nil {
		t.Fatal(err)
	}

	want := []float32{0.1, -0.2, 0.3, 1e-6, 99.5}
	if err := s.SetEmbedding(ctx, "user", "alpha", want, "test-model-v1"); err != nil {
		t.Fatal(err)
	}
	got, model, err := s.GetEmbedding(ctx, "user", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if model != "test-model-v1" {
		t.Errorf("model: got %q want test-model-v1", model)
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dim %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestSetEmbeddingNotFound(t *testing.T) {
	s := newStore(t)
	if err := s.SetEmbedding(context.Background(), "user", "missing", []float32{1, 2}, "m"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetEmbeddingNullByDefault(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Save(ctx, "user", "fresh", "value", ""); err != nil {
		t.Fatal(err)
	}
	vec, model, err := s.GetEmbedding(ctx, "user", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if vec != nil {
		t.Errorf("fresh row should have nil embedding, got %d-dim vec", len(vec))
	}
	if model != "" {
		t.Errorf("fresh row should have empty embedding_model, got %q", model)
	}
}

// SetEmbedding must not bump updated_at. The embedding is an index byproduct;
// "last modified" semantics should reflect content edits only.
func TestSetEmbeddingDoesNotTouchUpdatedAt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	m, err := s.Save(ctx, "user", "k", "v", "")
	if err != nil {
		t.Fatal(err)
	}
	originalUpdated := m.UpdatedAt
	time.Sleep(5 * time.Millisecond) // ensure clock moves
	if err := s.SetEmbedding(ctx, "user", "k", []float32{1, 2, 3}, "m"); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, "user", "k")
	if err != nil {
		t.Fatal(err)
	}
	if after.UpdatedAt != originalUpdated {
		t.Errorf("SetEmbedding bumped updated_at: %d -> %d", originalUpdated, after.UpdatedAt)
	}
}

func TestReembedAllSkipsAlreadyEmbedded(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Save(ctx, "user", k, "value-of-"+k, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Pre-embed b so reembed should skip it.
	if err := s.SetEmbedding(ctx, "user", "b", []float32{9, 9, 9, 9}, "old-model"); err != nil {
		t.Fatal(err)
	}

	var calls int64
	srv := makeFakeEmbedServer(t, fakeVec, &calls)
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)

	res, err := reembedAll(ctx, s, c, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 3 {
		t.Errorf("scanned: got %d want 3", res.Scanned)
	}
	if res.Embedded != 2 {
		t.Errorf("embedded: got %d want 2 (a, c)", res.Embedded)
	}
	if res.Skipped != 1 {
		t.Errorf("skipped: got %d want 1 (b had old-model embedding)", res.Skipped)
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("HTTP hits: got %d want 2 (b should not have called the endpoint)", got)
	}
	// Verify b kept its old embedding (skip means untouched).
	_, model, _ := s.GetEmbedding(ctx, "user", "b")
	if model != "old-model" {
		t.Errorf("b's model should still be old-model, got %q", model)
	}
}

func TestReembedAllForceReembedsAll(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		_, _ = s.Save(ctx, "user", k, "value-"+k, "")
		_ = s.SetEmbedding(ctx, "user", k, []float32{0, 0, 0, 0}, "stale-model")
	}

	var calls int64
	srv := makeFakeEmbedServer(t, fakeVec, &calls)
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "fresh-model", 2*time.Second)

	res, err := reembedAll(ctx, s, c, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedded != 2 || res.Skipped != 0 {
		t.Errorf("force should embed all: %+v", res)
	}
	// Both rows should now record the fresh model id.
	for _, k := range []string{"a", "b"} {
		_, model, _ := s.GetEmbedding(ctx, "user", k)
		if model != "fresh-model" {
			t.Errorf("%s: model=%q want fresh-model", k, model)
		}
	}
}

func TestReembedAllScopeFilter(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "u1", "in user", "")
	_, _ = s.Save(ctx, "feedback", "f1", "in feedback", "")
	_, _ = s.Save(ctx, "feedback", "f2", "also in feedback", "")

	var calls int64
	srv := makeFakeEmbedServer(t, fakeVec, &calls)
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)

	res, err := reembedAll(ctx, s, c, "feedback", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedded != 2 {
		t.Errorf("only feedback rows should embed: %+v", res)
	}
	// user/u1 must remain unembedded.
	_, model, _ := s.GetEmbedding(ctx, "user", "u1")
	if model != "" {
		t.Errorf("user/u1 should be untouched, got model=%q", model)
	}
}

func TestReembedAllCollectsErrorsAndContinues(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "ok1", "first", "")
	_, _ = s.Save(ctx, "user", "boom", "this one fails", "")
	_, _ = s.Save(ctx, "user", "ok2", "third", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Input, "fails") {
			http.Error(w, "synthetic", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []embedResponseItem{{Embedding: fakeVec(req.Input)}},
		})
	}))
	defer srv.Close()
	c := NewEmbedClient(srv.URL, "test-model", 2*time.Second)

	res, err := reembedAll(ctx, s, c, "", false)
	if err != nil {
		t.Fatalf("a per-row error must not abort the run: %v", err)
	}
	if res.Embedded != 2 {
		t.Errorf("two rows should still succeed: %+v", res)
	}
	if res.Errors != 1 {
		t.Errorf("one row should be marked error: %+v", res)
	}
	if len(res.ErrorDetail) != 1 || !strings.Contains(res.ErrorDetail[0], "boom") {
		t.Errorf("error detail should name the failing key: %+v", res.ErrorDetail)
	}
}

func TestReembedAllRefusesDisabledClient(t *testing.T) {
	s := newStore(t)
	c := NewEmbedClient("", "any", 0)
	_, err := reembedAll(context.Background(), s, c, "", false)
	if err != ErrEmbedDisabled {
		t.Errorf("expected ErrEmbedDisabled, got %v", err)
	}
}

func TestSaveAutoEmbedsWhenClientAttached(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var calls int64
	srv := makeFakeEmbedServer(t, fakeVec, &calls)
	defer srv.Close()
	s.SetEmbedClient(NewEmbedClient(srv.URL, "test-model", 2*time.Second))

	if _, err := s.Save(ctx, "user", "k", "auto-embed me", ""); err != nil {
		t.Fatal(err)
	}
	vec, model, err := s.GetEmbedding(ctx, "user", "k")
	if err != nil {
		t.Fatal(err)
	}
	if model != "test-model" {
		t.Errorf("model: got %q want test-model", model)
	}
	want := fakeVec("auto-embed me")
	if len(vec) != len(want) {
		t.Fatalf("len: got %d want %d", len(vec), len(want))
	}
	for i := range want {
		if vec[i] != want[i] {
			t.Errorf("dim %d: got %v want %v", i, vec[i], want[i])
		}
	}
	if atomic.LoadInt64(&calls) != 1 {
		t.Errorf("expected exactly 1 HTTP hit on save, got %d", calls)
	}
}

func TestSaveSucceedsEvenIfEmbedFails(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic", http.StatusInternalServerError)
	}))
	defer srv.Close()
	s.SetEmbedClient(NewEmbedClient(srv.URL, "test-model", 500*time.Millisecond))

	// Save MUST succeed even though the embed endpoint is broken.
	m, err := s.Save(ctx, "user", "k", "value-that-cannot-be-embedded", "")
	if err != nil {
		t.Fatalf("save should not surface embed errors: %v", err)
	}
	if m == nil || m.Key != "k" {
		t.Fatalf("save returned an unusable row: %+v", m)
	}
	// And the row stays embedding-less.
	vec, model, err := s.GetEmbedding(ctx, "user", "k")
	if err != nil {
		t.Fatal(err)
	}
	if vec != nil || model != "" {
		t.Errorf("row should be embedding-less after failed embed: vec=%d-dim model=%q", len(vec), model)
	}
}

func TestSaveWithoutEmbedClientIsSilent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// No SetEmbedClient call → s.embed is nil. Save must not crash and must
	// leave the row embedding-less.
	if _, err := s.Save(ctx, "user", "k", "no embedder configured", ""); err != nil {
		t.Fatal(err)
	}
	vec, model, err := s.GetEmbedding(ctx, "user", "k")
	if err != nil {
		t.Fatal(err)
	}
	if vec != nil || model != "" {
		t.Errorf("row should be embedding-less without a client: vec=%d-dim model=%q", len(vec), model)
	}
}

func TestSaveWithDisabledEmbedClientIsSilent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	s.SetEmbedClient(NewEmbedClient("", "any-model", 0)) // empty URL → disabled
	if _, err := s.Save(ctx, "user", "k", "disabled embedder", ""); err != nil {
		t.Fatal(err)
	}
	vec, _, _ := s.GetEmbedding(ctx, "user", "k")
	if vec != nil {
		t.Error("disabled client should not produce an embedding on save")
	}
}

// Updating a row should re-embed (the value changed). Pre-existing
// embeddings on the row should be replaced, not stacked.
func TestSaveUpdatesEmbeddingOnValueChange(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var calls int64
	srv := makeFakeEmbedServer(t, fakeVec, &calls)
	defer srv.Close()
	s.SetEmbedClient(NewEmbedClient(srv.URL, "test-model", 2*time.Second))

	if _, err := s.Save(ctx, "user", "k", "first version", ""); err != nil {
		t.Fatal(err)
	}
	v1, _, _ := s.GetEmbedding(ctx, "user", "k")

	if _, err := s.Save(ctx, "user", "k", "second version with different content", ""); err != nil {
		t.Fatal(err)
	}
	v2, _, _ := s.GetEmbedding(ctx, "user", "k")

	if len(v2) == 0 {
		t.Fatal("update should have produced a new embedding")
	}
	// fakeVec is deterministic per input; v1 and v2 must differ.
	same := len(v1) == len(v2)
	if same {
		for i := range v1 {
			if v1[i] != v2[i] {
				same = false
				break
			}
		}
	}
	if same {
		t.Error("embedding should change when value changes; got identical bytes")
	}
	if atomic.LoadInt64(&calls) != 2 {
		t.Errorf("expected 2 HTTP hits (one per save), got %d", calls)
	}
}

// ----- Phase 1.C: vector search + RRF blend tests --------------------------

func TestCosineBasics(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1.0},        // identical
		{[]float32{1, 0}, []float32{-1, 0}, -1.0},      // antiparallel
		{[]float32{1, 0}, []float32{0, 1}, 0.0},        // orthogonal
		{[]float32{}, []float32{1, 2}, 0.0},            // empty
		{[]float32{0, 0}, []float32{1, 2}, 0.0},        // zero norm
		{[]float32{1, 2, 3}, []float32{1, 2}, 0.0},     // dim mismatch
	}
	for _, c := range cases {
		got := cosine(c.a, c.b)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("cosine(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestVectorSearchTopKByCosine(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Seed three memories with hand-crafted embeddings that have a known
	// rank against a target query vector.
	rows := []struct {
		key string
		vec []float32
	}{
		{"perfect_match", []float32{1, 0, 0}},
		{"orthogonal", []float32{0, 1, 0}},
		{"close_match", []float32{0.9, 0.1, 0.0}},
	}
	for _, r := range rows {
		if _, err := s.Save(ctx, "user", r.key, "v "+r.key, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.SetEmbedding(ctx, "user", r.key, r.vec, "test-model"); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := s.VectorSearch(ctx, "", []float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	wantOrder := []string{"perfect_match", "close_match", "orthogonal"}
	for i, want := range wantOrder {
		if hits[i].Key != want {
			t.Errorf("hit %d: got %q want %q (full order: %+v)", i, hits[i].Key, want, keysOf(hits))
		}
	}
	// Score should equal the cosine; perfect_match must be ~1.0.
	if math.Abs(hits[0].Score-1.0) > 1e-6 {
		t.Errorf("perfect_match score should be 1.0, got %v", hits[0].Score)
	}
}

func TestVectorSearchSkipsUnembeddedAndArchived(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// One embedded, one un-embedded, one embedded-but-archived.
	for _, k := range []string{"embedded", "unembedded", "archived"} {
		_, _ = s.Save(ctx, "user", k, k, "")
	}
	_ = s.SetEmbedding(ctx, "user", "embedded", []float32{1, 0, 0}, "m")
	_ = s.SetEmbedding(ctx, "user", "archived", []float32{1, 0, 0}, "m")
	if _, err := s.Supersede(ctx, "user", "archived", "successor", "v", ""); err != nil {
		t.Fatal(err)
	}

	hits, err := s.VectorSearch(ctx, "", []float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Key != "embedded" {
		t.Fatalf("vector search must skip unembedded + archived rows, got %+v", keysOf(hits))
	}
}

func TestVectorSearchRespectsScopeFilter(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "u1", "in user", "")
	_, _ = s.Save(ctx, "feedback", "f1", "in feedback", "")
	_ = s.SetEmbedding(ctx, "user", "u1", []float32{1, 0, 0}, "m")
	_ = s.SetEmbedding(ctx, "feedback", "f1", []float32{1, 0, 0}, "m")

	hits, _ := s.VectorSearch(ctx, "feedback", []float32{1, 0, 0}, 10)
	if len(hits) != 1 || hits[0].Key != "f1" {
		t.Errorf("scope filter not respected: %+v", keysOf(hits))
	}
}

func TestVectorSearchSkipsDimMismatch(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "compatible", "v", "")
	_, _ = s.Save(ctx, "user", "stale_dim", "v", "")
	_ = s.SetEmbedding(ctx, "user", "compatible", []float32{1, 0, 0}, "m")
	_ = s.SetEmbedding(ctx, "user", "stale_dim", []float32{1, 0, 0, 0, 0}, "old-model")

	hits, _ := s.VectorSearch(ctx, "", []float32{1, 0, 0}, 10)
	if len(hits) != 1 || hits[0].Key != "compatible" {
		t.Errorf("dim mismatch should be skipped, got %+v", keysOf(hits))
	}
}

func TestRRFBlendCombinesBothSources(t *testing.T) {
	// FTS finds A, B, C in that order. Vec finds B, A, D. With k=60,
	// expected RRF totals (descending):
	//   A: 1/61 + 1/62 = 0.03250
	//   B: 1/62 + 1/61 = 0.03250 (tie with A; pinned/updated breaks)
	//   C: 1/63        = 0.01587
	//   D: 1/63        = 0.01587 (tie with C)
	mk := func(key string, pinned bool, updated int64) SearchHit {
		return SearchHit{Scope: "user", Key: key, Pinned: pinned, UpdatedAt: updated}
	}
	fts := []SearchHit{mk("A", false, 100), mk("B", true, 200), mk("C", false, 50)}
	vec := []SearchHit{mk("B", true, 200), mk("A", false, 100), mk("D", false, 75)}

	out := rrfBlend(fts, vec, 60, 10)
	if len(out) != 4 {
		t.Fatalf("expected 4 unique hits, got %d: %+v", len(out), keysOf(out))
	}
	// A and B tie; B has Pinned=true → wins the tie-break.
	if out[0].Key != "B" || out[1].Key != "A" {
		t.Errorf("pinned should break a score tie: %+v", keysOf(out))
	}
	// C and D tie at 1/63; D has higher updated_at → wins.
	if out[2].Key != "D" || out[3].Key != "C" {
		t.Errorf("updated_at should break the next tie: %+v", keysOf(out))
	}
}

func TestRRFBlendUnionsExclusiveHits(t *testing.T) {
	mk := func(key string) SearchHit { return SearchHit{Scope: "user", Key: key} }
	fts := []SearchHit{mk("only_fts"), mk("both")}
	vec := []SearchHit{mk("only_vec"), mk("both")}

	out := rrfBlend(fts, vec, 60, 10)
	keys := map[string]bool{}
	for _, h := range out {
		keys[h.Key] = true
	}
	for _, want := range []string{"only_fts", "only_vec", "both"} {
		if !keys[want] {
			t.Errorf("missing %q from RRF result: %+v", want, keysOf(out))
		}
	}
}

func TestRRFBlendRespectsLimit(t *testing.T) {
	mk := func(key string) SearchHit { return SearchHit{Scope: "user", Key: key} }
	hits := make([]SearchHit, 20)
	for i := range hits {
		hits[i] = mk(fmt.Sprintf("k%02d", i))
	}
	out := rrfBlend(hits, nil, 60, 5)
	if len(out) != 5 {
		t.Errorf("limit not enforced, got %d", len(out))
	}
}

func TestSearchCrossLingualHybrid(t *testing.T) {
	// The smoking-gun test for the whole feature: an FTS-token-mismatched
	// query must still find its semantic target via the vector path. The
	// fake embedder uses cosine of fixed vectors that we plant.
	s := newStore(t)
	ctx := context.Background()

	// Two memories. Their FTS-searchable bodies share zero query tokens
	// with the IT-shaped query; only the embedding bridge can find them.
	_, _ = s.Save(ctx, "user", "wishlist", "improve memory ideas", "")
	_, _ = s.Save(ctx, "user", "unrelated", "caddy webserver config", "")

	wishVec := []float32{1, 0, 0}
	otherVec := []float32{0, 1, 0}
	_ = s.SetEmbedding(ctx, "user", "wishlist", wishVec, "m")
	_ = s.SetEmbedding(ctx, "user", "unrelated", otherVec, "m")

	// Stub embedder: maps query strings to known vectors so we can predict
	// vector ranks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// "idee migliore memoria" must map to the same direction as wishVec.
		var v []float32
		if strings.Contains(req.Input, "idee") {
			v = []float32{0.95, 0.05, 0}
		} else {
			v = []float32{0, 0.5, 0.5}
		}
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []embedResponseItem{{Embedding: v}},
		})
	}))
	defer srv.Close()
	s.SetEmbedClient(NewEmbedClient(srv.URL, "m", 2*time.Second))

	hits, err := s.Search(ctx, "", "idee migliore memoria", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("hybrid search should not return empty for a vector-bridgeable query")
	}
	if hits[0].Key != "wishlist" {
		t.Errorf("hybrid should rank vector-aligned 'wishlist' first, got order %+v", keysOf(hits))
	}
}

func TestSearchFallsBackToFTSWhenEmbedFails(t *testing.T) {
	// FTS path must still produce results when the embedder errors.
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "fts_target", "alpha bravo charlie", "")

	// Embedder returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic", http.StatusInternalServerError)
	}))
	defer srv.Close()
	s.SetEmbedClient(NewEmbedClient(srv.URL, "m", 500*time.Millisecond))

	hits, err := s.Search(ctx, "", "alpha", 10)
	if err != nil {
		t.Fatalf("search must not error when embed errors: %v", err)
	}
	if len(hits) != 1 || hits[0].Key != "fts_target" {
		t.Errorf("FTS-only fallback failed, got %+v", keysOf(hits))
	}
}

func TestSearchPureFTSWhenNoEmbedClient(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Save(ctx, "user", "fts_target", "delta echo foxtrot", "")
	// No SetEmbedClient call.
	hits, err := s.Search(ctx, "", "delta", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Key != "fts_target" {
		t.Errorf("nil-client path should match existing FTS behaviour, got %+v", keysOf(hits))
	}
}

func TestSearchHybridDoesNotRegressTokenExactRanking(t *testing.T) {
	// A query with a clear token-exact match must keep that match at #1
	// even when the vector path proposes alternatives. RRF naturally
	// favours docs that hit both lists; the token-exact doc usually does.
	s := newStore(t)
	ctx := context.Background()

	_, _ = s.Save(ctx, "user", "token_match", "the term needle is right here", "")
	_, _ = s.Save(ctx, "user", "semantic_neighbor", "loosely related sewing topic", "")
	_ = s.SetEmbedding(ctx, "user", "token_match", []float32{0.6, 0.8, 0.0}, "m")
	_ = s.SetEmbedding(ctx, "user", "semantic_neighbor", []float32{0.7, 0.7, 0.1}, "m")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Query embedding closer to semantic_neighbor than token_match,
		// so a vec-only ranker would put semantic_neighbor first.
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []embedResponseItem{{Embedding: []float32{0.7, 0.7, 0.1}}},
		})
	}))
	defer srv.Close()
	s.SetEmbedClient(NewEmbedClient(srv.URL, "m", 2*time.Second))

	hits, err := s.Search(ctx, "", "needle", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 || hits[0].Key != "token_match" {
		t.Errorf("RRF should keep token-exact #1 over vec-only neighbour, got %+v", keysOf(hits))
	}
}

// Sanity: a stored row should be retrievable by both Get (unaffected) and
// GetEmbedding (new). We don't want adding the columns to break the existing
// memory shape.
func TestGetStillReturnsNonEmbeddingFields(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	in, err := s.Save(ctx, "user", "x", fmt.Sprintf("body-%d", time.Now().UnixNano()), "tag1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEmbedding(ctx, "user", "x", []float32{1, 2}, "model"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Get(ctx, "user", "x")
	if err != nil {
		t.Fatal(err)
	}
	if out.Value != in.Value || out.Tags != in.Tags {
		t.Errorf("Get drift: in=%+v out=%+v", in, out)
	}
}
