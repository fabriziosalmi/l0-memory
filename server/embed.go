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
	"sort"
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

// cosine returns the cosine similarity between a and b. Returns 0 when
// either vector is empty or has zero norm. Vectors of different length
// also return 0 (a model swap that produced different-dim BLOBs should
// not silently be compared as if they were compatible).
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// VectorSearch returns the rows whose embedding has the highest cosine
// similarity to qvec, top `limit` only. Skips rows without an embedding,
// archived rows, and rows whose embedding has a different dimensionality
// than qvec (model-swap safety). When `scope` is empty, searches every
// scope; otherwise filters as `memories.scope = ?`.
//
// Implementation is a flat scan: pull the embedding column of every
// candidate row into Go and compute cosine in-process. At the user's
// scale (tens of memories) this is microseconds; at 10k memories it's
// ~15ms — still cheaper than the network roundtrip for the query
// embedding itself. Add ANN/sqlite-vec only when it actually matters.
func (s *Store) VectorSearch(ctx context.Context, scope string, qvec []float32, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	if len(qvec) == 0 {
		return []SearchHit{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.scope, m.key, m.tags, m.pinned, m.archived, m.origin, m.verified_at,
			length(CAST(m.value AS BLOB)) AS size_bytes,
			m.created_at, m.updated_at,
			m.value, m.embedding
		FROM memories m
		WHERE m.embedding IS NOT NULL
		  AND m.archived = 0
		  AND (?1 = '' OR m.scope = ?1)
	`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []scoredHit
	for rows.Next() {
		var sc scoredHit
		var pinnedInt, archivedInt int
		var blob []byte
		if err := rows.Scan(
			&sc.hit.ID, &sc.hit.Scope, &sc.hit.Key, &sc.hit.Tags,
			&pinnedInt, &archivedInt, &sc.hit.Origin, &sc.hit.VerifiedAt,
			&sc.hit.SizeBytes, &sc.hit.CreatedAt, &sc.hit.UpdatedAt,
			&sc.val, &blob,
		); err != nil {
			return nil, err
		}
		sc.hit.Pinned = pinnedInt != 0
		sc.hit.Archived = archivedInt != 0
		vec, err := decodeEmbedding(blob)
		if err != nil {
			// One bad row should not poison the whole search.
			continue
		}
		if len(vec) != len(qvec) {
			continue
		}
		sc.sim = cosine(qvec, vec)
		all = append(all, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Descending by similarity; ties broken by id (newer rows score later
	// in the same cosine bucket — a rare corner because float32 ties are
	// extremely unlikely with EmbeddingGemma-shaped outputs).
	sort.Slice(all, func(i, j int) bool {
		if all[i].sim != all[j].sim {
			return all[i].sim > all[j].sim
		}
		return all[i].hit.ID > all[j].hit.ID
	})

	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]SearchHit, len(all))
	for i, sc := range all {
		sc.hit.Score = sc.sim
		// Build a tiny snippet from the value so the caller has something
		// to display without re-fetching. Reuses the FTS-side preview path.
		sc.hit.Snippet = preview(sc.val, 200)
		out[i] = sc.hit
	}
	return out, nil
}

// scoredHit pairs a search hit with its raw value (for snippet creation)
// and the cosine similarity that produced its rank. Package-scoped (not
// inlined) so it can be the element type of a slice passed to sort.Slice.
type scoredHit struct {
	hit SearchHit
	val string
	sim float64
}

// rrfBlend merges FTS and vector hits via Reciprocal Rank Fusion.
// `k` is the standard RRF damping constant (60 is the canonical value;
// lower amplifies rank differences, higher flattens them).
//
// Each (scope, key) gets an RRF score of:
//   sum over sources of 1 / (k + 1-based rank in that source)
//
// Hits that appear in only one source contribute only that source's
// term, so a strong vector hit can still win over an absent-from-FTS
// peer, and vice versa. The output is sorted by RRF descending, with
// pinned-first acting as a tie-breaker (not an override) — within a
// query, semantic relevance is the primary signal; pin is a secondary
// hint about user intent that breaks otherwise-equal ties.
func rrfBlend(fts, vec []SearchHit, k int, limit int) []SearchHit {
	if k <= 0 {
		k = 60
	}
	if limit <= 0 {
		limit = 50
	}
	type acc struct {
		hit   SearchHit
		score float64
	}
	bag := map[string]*acc{}
	uri := func(h SearchHit) string { return h.Scope + "\x00" + h.Key }

	for i, h := range fts {
		k1 := uri(h)
		entry := bag[k1]
		if entry == nil {
			entry = &acc{hit: h}
			bag[k1] = entry
		}
		entry.score += 1.0 / float64(k+i+1)
	}
	for i, h := range vec {
		k1 := uri(h)
		entry := bag[k1]
		if entry == nil {
			// First time we see this row; carry the vec snippet/score
			// into the merged shape. The Score field gets overwritten
			// below with the RRF total.
			entry = &acc{hit: h}
			bag[k1] = entry
		} else {
			// Prefer the FTS snippet (it's highlighted) but pick the
			// vector snippet if FTS didn't have one.
			if entry.hit.Snippet == "" {
				entry.hit.Snippet = h.Snippet
			}
		}
		entry.score += 1.0 / float64(k+i+1)
	}

	out := make([]SearchHit, 0, len(bag))
	for _, e := range bag {
		e.hit.Score = e.score
		out = append(out, e.hit)
	}
	// Composite descending order: RRF score, then pinned, then newest.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
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
