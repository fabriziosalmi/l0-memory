package main

// embed.go — OpenAI-compatible /v1/embeddings client and BLOB encoding helpers.
//
// Configuration is environment-driven so the binary stays drop-in:
//
//	LTM_EMBEDDING_URL    HTTP endpoint, e.g. http://localhost:11434/v1/embeddings
//	                     (Ollama, llmproxy, LM Studio, vLLM, OpenAI all conform).
//	                     Empty / unset → embedding disabled, FTS-only path.
//	LTM_EMBEDDING_MODEL  Model id, e.g. text-embedding-embeddinggemma-300m.
//	LTM_EMBED_DISABLE    "1" to force-disable even when URL is set.
//	LTM_EMBED_TIMEOUT    Per-request timeout, default 5s. Format: Go duration.
//
// On-disk encoding for the `embedding` BLOB column is little-endian float32:
// 4 bytes per dimension, no header, no length prefix. Dimensionality is
// inferred from `len(blob) / 4`. The companion `embedding_model` column
// records which model produced the bytes so dim/version mismatches can be
// detected without per-row metadata.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// ErrEmbedDisabled is returned when the client is configured off (no URL or
// LTM_EMBED_DISABLE=1). Callers should treat this as a soft signal — save
// proceeds, the row stays embedding-less and is searchable only via FTS.
var ErrEmbedDisabled = errors.New("embedding disabled")

// EmbedClient calls an OpenAI-compatible /v1/embeddings endpoint. Zero value
// is the disabled client; use NewEmbedClient or NewEmbedClientFromEnv.
type EmbedClient struct {
	url     string
	model   string
	http    *http.Client
	timeout time.Duration
}

// NewEmbedClient builds a client with explicit config. Pass url="" for the
// disabled client (Embed will return ErrEmbedDisabled).
func NewEmbedClient(url, model string, timeout time.Duration) *EmbedClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &EmbedClient{
		url:     strings.TrimSpace(url),
		model:   strings.TrimSpace(model),
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

// NewEmbedClientFromEnv reads LTM_EMBEDDING_URL / LTM_EMBEDDING_MODEL /
// LTM_EMBED_DISABLE / LTM_EMBED_TIMEOUT and returns a configured client.
// Always returns a non-nil client; check Disabled() to know if Embed will
// short-circuit.
func NewEmbedClientFromEnv() *EmbedClient {
	url := os.Getenv("LTM_EMBEDDING_URL")
	model := os.Getenv("LTM_EMBEDDING_MODEL")
	if os.Getenv("LTM_EMBED_DISABLE") == "1" {
		url = ""
	}
	timeout := 5 * time.Second
	if t := os.Getenv("LTM_EMBED_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil && d > 0 {
			timeout = d
		}
	}
	return NewEmbedClient(url, model, timeout)
}

// Disabled reports whether Embed will immediately return ErrEmbedDisabled.
func (c *EmbedClient) Disabled() bool {
	return c == nil || c.url == ""
}

// Model returns the configured model id (used to populate the
// embedding_model column at write time).
func (c *EmbedClient) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

// embedRequest / embedResponse mirror the OpenAI embeddings API surface.
type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponseItem struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object"`
}

type embedResponse struct {
	Data  []embedResponseItem `json:"data"`
	Model string              `json:"model"`
}

// Embed encodes `text` into a float32 vector via the configured endpoint.
// Returns ErrEmbedDisabled when the client is off; otherwise propagates
// transport / decoding errors verbatim so the caller can decide whether to
// retry or skip.
func (c *EmbedClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.Disabled() {
		return nil, ErrEmbedDisabled
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("embed: empty input")
	}
	body, err := json.Marshal(embedRequest{Model: c.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}

	// Honour both the per-call ctx deadline and the client timeout. Whichever
	// fires first wins.
	cctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Slurp a small prefix to surface upstream error bodies for
		// diagnostics without ballooning logs on a 4xx with HTML payload.
		buf := make([]byte, 512)
		n, _ := io.ReadFull(resp.Body, buf)
		return nil, fmt.Errorf("embed: %s: %s", resp.Status, strings.TrimSpace(string(buf[:n])))
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, errors.New("embed: empty response")
	}
	return out.Data[0].Embedding, nil
}

// encodeEmbedding packs a float32 slice as little-endian bytes (4 per dim).
// Returns nil for nil input so callers can pass through "no embedding".
func encodeEmbedding(vec []float32) []byte {
	if vec == nil {
		return nil
	}
	out := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

// decodeEmbedding reverses encodeEmbedding. Returns nil for nil/empty input.
// On a malformed payload (length not a multiple of 4) returns an error
// rather than silently truncating.
func decodeEmbedding(blob []byte) ([]float32, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("decodeEmbedding: %d bytes is not a multiple of 4", len(blob))
	}
	dim := len(blob) / 4
	out := make([]float32, dim)
	for i := 0; i < dim; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[4*i:]))
	}
	return out, nil
}

// SetEmbedding persists a vector + producing model on (scope, key). Does NOT
// touch updated_at — embedding is an index byproduct, not a content edit, so
// it would mislead "last modified" displays. ErrNotFound when no such row.
func (s *Store) SetEmbedding(ctx context.Context, scope, key string, vec []float32, model string) error {
	scope = resolveScope(scope)
	res, err := s.db.ExecContext(ctx, `
		UPDATE memories
		SET embedding = ?, embedding_model = ?
		WHERE scope = ? AND key = ?
	`, encodeEmbedding(vec), model, scope, key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReembedResult summarises a backfill run for human-readable output.
type ReembedResult struct {
	Scanned     int      `json:"scanned"`
	Embedded    int      `json:"embedded"`
	Skipped     int      `json:"skipped"`     // already had an embedding (and --force not set)
	Errors      int      `json:"errors"`
	ErrorDetail []string `json:"error_detail,omitempty"`
}

// reembedAll iterates memories in the given scope (empty = all scopes),
// computes an embedding for each row that lacks one (or all rows when
// force=true), and persists the result. Returns aggregate counters; per-row
// errors are collected without aborting the whole run, so a transient
// endpoint hiccup on row 7 doesn't lose the work done on rows 1–6.
//
// Designed for the CLI `ltm reembed` and for tests; it does not log to
// stdout. The CLI wrapper formats progress separately.
func reembedAll(ctx context.Context, s *Store, c *EmbedClient, scope string, force bool) (*ReembedResult, error) {
	if c.Disabled() {
		return nil, ErrEmbedDisabled
	}
	// Use the existing list shape — pinned-first, all scopes when scope=="",
	// archived hidden by default. Embeddings on archived rows are dead
	// weight; skipping them mirrors the search-time filter.
	rows, err := s.List(ctx, scope, 100000)
	if err != nil {
		return nil, err
	}
	res := &ReembedResult{}
	for _, m := range rows {
		res.Scanned++
		if !force {
			// Cheap pre-check: a non-empty embedding_model column means the
			// row has been embedded at some point. Avoids re-fetching the
			// BLOB just to test for presence.
			_, mdl, err := s.GetEmbedding(ctx, m.Scope, m.Key)
			if err == nil && mdl != "" {
				res.Skipped++
				continue
			}
		}
		vec, err := c.Embed(ctx, m.Value)
		if err != nil {
			res.Errors++
			res.ErrorDetail = append(res.ErrorDetail,
				fmt.Sprintf("%s/%s: %v", m.Scope, m.Key, err))
			continue
		}
		if err := s.SetEmbedding(ctx, m.Scope, m.Key, vec, c.Model()); err != nil {
			res.Errors++
			res.ErrorDetail = append(res.ErrorDetail,
				fmt.Sprintf("%s/%s: persist: %v", m.Scope, m.Key, err))
			continue
		}
		res.Embedded++
	}
	return res, nil
}

// GetEmbedding returns the stored vector and the model id that produced it,
// or (nil, "", nil) when the row exists but has no embedding yet. Returns
// ErrNotFound when no such row.
func (s *Store) GetEmbedding(ctx context.Context, scope, key string) ([]float32, string, error) {
	scope = resolveScope(scope)
	var blob []byte
	var model string
	err := s.db.QueryRowContext(ctx, `
		SELECT embedding, embedding_model FROM memories WHERE scope = ? AND key = ?
	`, scope, key).Scan(&blob, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	vec, err := decodeEmbedding(blob)
	if err != nil {
		return nil, "", err
	}
	return vec, model, nil
}
