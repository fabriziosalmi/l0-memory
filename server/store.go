package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

type Memory struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Tags      string `json:"tags"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// SearchHit is the compact result of a search: enough metadata for the
// caller to decide whether to load the full record, plus a contextual
// snippet so the value is usually unnecessary.
type SearchHit struct {
	ID        int64   `json:"id"`
	Key       string  `json:"key"`
	Tags      string  `json:"tags"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
	SizeBytes int     `json:"size_bytes"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

// ErrNotFound is returned by Get when no row matches the key.
var ErrNotFound = errors.New("memory not found")

func defaultDBPath() (string, error) {
	if p := os.Getenv("LTM_DB"); p != "" {
		if dir := filepath.Dir(p); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
		}
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".long-term-memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "memories.db"), nil
}

func OpenStore() (*Store, error) {
	path, err := defaultDBPath()
	if err != nil {
		return nil, err
	}
	return openStoreAt(path)
}

func openStoreAt(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite is goroutine-safe but a single writer connection avoids
	// "database is locked" errors under WAL with concurrent writers.
	db.SetMaxOpenConns(1)
	schema := `
	CREATE TABLE IF NOT EXISTS memories (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		key        TEXT UNIQUE NOT NULL,
		value      TEXT NOT NULL,
		tags       TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_memories_updated ON memories(updated_at DESC);

	-- Full-text search index. Standalone FTS5 table (data lives in this
	-- virtual table, not external-content). Triggers below keep it in sync
	-- with the memories table. Storage cost is small for typical memory
	-- payloads and buys us a much simpler ops model.
	CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
		key, value, tags,
		tokenize='unicode61 remove_diacritics 2'
	);

	CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
		INSERT INTO memories_fts(rowid, key, value, tags)
		VALUES (new.id, new.key, new.value, new.tags);
	END;
	CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
		DELETE FROM memories_fts WHERE rowid = old.id;
	END;
	CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
		DELETE FROM memories_fts WHERE rowid = old.id;
		INSERT INTO memories_fts(rowid, key, value, tags)
		VALUES (new.id, new.key, new.value, new.tags);
	END;
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Migration: backfill the FTS index for rows that pre-date the index
	// (existing DBs created before FTS5 was introduced).
	if err := backfillFTS(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("fts backfill: %w", err)
	}

	return &Store{db: db}, nil
}

// backfillFTS repopulates memories_fts when it is out of sync with memories.
// Typical case: a DB created before FTS5 was wired in — the virtual table
// gets created on open but is empty. We simply re-insert the missing rows.
func backfillFTS(db *sql.DB) error {
	var memCount, ftsCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&memCount); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM memories_fts`).Scan(&ftsCount); err != nil {
		return err
	}
	if memCount == ftsCount {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO memories_fts(rowid, key, value, tags)
		SELECT m.id, m.key, m.value, m.tags
		FROM memories m
		WHERE m.id NOT IN (SELECT rowid FROM memories_fts)
	`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Save(ctx context.Context, key, value, tags string) (*Memory, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memories (key, value, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			tags = excluded.tags,
			updated_at = excluded.updated_at
	`, key, value, tags, now, now)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, key)
}

func (s *Store) Get(ctx context.Context, key string) (*Memory, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, key, value, tags, created_at, updated_at FROM memories WHERE key = ?`, key)
	var m Memory
	if err := row.Scan(&m.ID, &m.Key, &m.Value, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (s *Store) Delete(ctx context.Context, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE key = ?`, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) List(ctx context.Context, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, key, value, tags, created_at, updated_at FROM memories ORDER BY updated_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Key, &m.Value, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// escapeLike escapes the LIKE wildcards so user input is treated literally.
// We use '\' as the ESCAPE character — the SQL queries below declare it.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ftsQuery turns a free-form user query into an FTS5 MATCH expression with
// a friendly default: each whitespace-separated token becomes a prefix match
// AND'd with the others. Tokens containing FTS5 special characters are
// quoted as phrases (no prefix). Returns "" if there is no usable token,
// which signals the caller to fall back to LIKE.
func ftsQuery(raw string) string {
	parts := strings.Fields(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if isAlphaNum(p) {
			// Bareword + prefix: no quoting needed.
			out = append(out, p+"*")
			continue
		}
		// Quote as phrase. Inner double quotes are escaped by doubling.
		quoted := `"` + strings.ReplaceAll(p, `"`, `""`) + `"`
		out = append(out, quoted)
	}
	return strings.Join(out, " ")
}

func isAlphaNum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Search returns ranked, snippet-bearing hits without the full value. It is
// the right entry point for LLMs and the MCP `memory_search` default. Use
// SearchExpanded when the caller really needs the value field.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(query) == "" {
		return []SearchHit{}, nil
	}
	if expr := ftsQuery(query); expr != "" {
		if out, err := s.searchFTSCompact(ctx, expr, limit); err == nil {
			return out, nil
		}
	}
	return s.searchLikeCompact(ctx, query, limit)
}

// SearchExpanded returns the same ranking as Search but with full Memory
// records (value included). Used by the CLI and by MCP when expand:true.
func (s *Store) SearchExpanded(ctx context.Context, query string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(query) == "" {
		return []Memory{}, nil
	}
	if expr := ftsQuery(query); expr != "" {
		if out, err := s.searchFTSExpanded(ctx, expr, limit); err == nil {
			return out, nil
		}
	}
	return s.searchLikeExpanded(ctx, query, limit)
}

func (s *Store) searchFTSCompact(ctx context.Context, expr string, limit int) ([]SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.key, m.tags,
			-bm25(memories_fts) AS score,
			snippet(memories_fts, -1, '<<', '>>', '...', 12) AS snip,
			length(CAST(m.value AS BLOB)) AS size_bytes,
			m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?
		ORDER BY f.rank, m.updated_at DESC, m.id DESC
		LIMIT ?
	`, expr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.Key, &h.Tags, &h.Score, &h.Snippet, &h.SizeBytes, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) searchFTSExpanded(ctx context.Context, expr string, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.key, m.value, m.tags, m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?
		ORDER BY f.rank, m.updated_at DESC, m.id DESC
		LIMIT ?
	`, expr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Key, &m.Value, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) searchLikeCompact(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	full, err := s.searchLikeExpanded(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, len(full))
	for i, m := range full {
		out[i] = SearchHit{
			ID:        m.ID,
			Key:       m.Key,
			Tags:      m.Tags,
			Score:     0, // LIKE has no ranking signal.
			Snippet:   makeLikeSnippet(query, m, 100),
			SizeBytes: len(m.Value),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		}
	}
	return out, nil
}

func (s *Store) searchLikeExpanded(ctx context.Context, query string, limit int) ([]Memory, error) {
	q := "%" + strings.ToLower(escapeLike(query)) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, key, value, tags, created_at, updated_at
		FROM memories
		WHERE LOWER(key) LIKE ? ESCAPE '\'
		   OR LOWER(value) LIKE ? ESCAPE '\'
		   OR LOWER(tags) LIKE ? ESCAPE '\'
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, q, q, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.Key, &m.Value, &m.Tags, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// makeLikeSnippet builds an FTS-style snippet for the LIKE fallback path:
// up to maxLen characters around the first match, with `<<…>>` highlight.
// It checks value, then key, then tags, in that order. UTF-8 boundaries
// are not preserved exactly — the LIKE path is best-effort by design.
func makeLikeSnippet(query string, m Memory, maxLen int) string {
	for _, src := range []string{m.Value, m.Key, m.Tags} {
		if snip := snippetFromText(query, src, maxLen); snip != "" {
			return snip
		}
	}
	return preview(m.Value, maxLen)
}

func snippetFromText(query, source string, maxLen int) string {
	if query == "" || source == "" {
		return ""
	}
	lowered := strings.ToLower(source)
	q := strings.ToLower(query)
	idx := strings.Index(lowered, q)
	if idx < 0 {
		return ""
	}
	start := idx - maxLen/2
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(source) {
		end = len(source)
	}
	if end < idx+len(query) {
		end = idx + len(query)
	}
	snip := source[start:end]
	matchInSnip := idx - start
	if matchInSnip >= 0 && matchInSnip+len(query) <= len(snip) {
		snip = snip[:matchInSnip] + "<<" + snip[matchInSnip:matchInSnip+len(query)] + ">>" + snip[matchInSnip+len(query):]
	}
	if start > 0 {
		snip = "..." + snip
	}
	if end < len(source) {
		snip = snip + "..."
	}
	return snip
}

// Count returns the total number of memories. Useful for tests and health checks.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&n)
	return n, err
}
