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
