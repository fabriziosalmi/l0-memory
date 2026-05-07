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

// DefaultScope is used by save/get/delete when the caller doesn't specify
// one. List/Search treat empty scope as "all scopes" instead.
const DefaultScope = "user"

type Memory struct {
	ID        int64  `json:"id"`
	Scope     string `json:"scope"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Tags      string `json:"tags"`
	Pinned    bool   `json:"pinned"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// SearchHit is the compact result of a search: enough metadata for the
// caller to decide whether to load the full record, plus a contextual
// snippet so the value is usually unnecessary.
type SearchHit struct {
	ID        int64   `json:"id"`
	Scope     string  `json:"scope"`
	Key       string  `json:"key"`
	Tags      string  `json:"tags"`
	Pinned    bool    `json:"pinned"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
	SizeBytes int     `json:"size_bytes"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

// ErrNotFound is returned by Get when no row matches the (scope, key) pair.
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
	// modernc.org/sqlite is goroutine-safe but a single writer connection
	// avoids "database is locked" errors under WAL with concurrent writers.
	db.SetMaxOpenConns(1)

	// 1. Bootstrap-shape table — keeps a pre-0.2 layout from blocking
	//    migrateSchema. The full set of indices and triggers is created in
	//    step 3, after migration, when the columns are guaranteed to exist.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			scope      TEXT NOT NULL DEFAULT 'user',
			key        TEXT NOT NULL,
			value      TEXT NOT NULL,
			tags       TEXT NOT NULL DEFAULT '',
			pinned     INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(scope, key)
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			key, value, tags,
			tokenize='unicode61 remove_diacritics 2'
		);
	`); err != nil {
		_ = db.Close()
		return nil, err
	}

	// 2. Bring legacy schemas up to date before referencing the new columns.
	if err := migrateSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("schema migration: %w", err)
	}

	// 3. Now safe: indices on `pinned` and triggers wiring memories_fts.
	if _, err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_memories_updated ON memories(updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_memories_pinned  ON memories(pinned, updated_at DESC);

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
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("post-migration schema: %w", err)
	}

	if err := backfillFTS(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("fts backfill: %w", err)
	}

	return &Store{db: db}, nil
}

// migrateSchema upgrades pre-0.2 databases (no scope/pinned columns) to the
// current schema. Idempotent: a no-op once the columns are present.
func migrateSchema(db *sql.DB) error {
	hasScope, hasPinned, err := tableHasColumns(db, "memories", "scope", "pinned")
	if err != nil {
		return err
	}
	if hasScope && hasPinned {
		return nil
	}

	// SQLite cannot change a UNIQUE constraint with ALTER, so we rebuild.
	// The new memories table got created by the openStoreAt schema; if it
	// exists with the old shape, that CREATE was a no-op. We rebuild here.
	steps := []string{
		`DROP TRIGGER IF EXISTS memories_ai`,
		`DROP TRIGGER IF EXISTS memories_au`,
		`DROP TRIGGER IF EXISTS memories_ad`,
		`ALTER TABLE memories RENAME TO memories_old`,
		`CREATE TABLE memories (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			scope      TEXT NOT NULL DEFAULT 'user',
			key        TEXT NOT NULL,
			value      TEXT NOT NULL,
			tags       TEXT NOT NULL DEFAULT '',
			pinned     INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(scope, key)
		)`,
		`INSERT INTO memories (id, scope, key, value, tags, pinned, created_at, updated_at)
		 SELECT id, 'user', key, value, tags, 0, created_at, updated_at FROM memories_old`,
		`DROP TABLE memories_old`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated ON memories(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_pinned ON memories(pinned, updated_at DESC)`,
		`CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, key, value, tags)
			VALUES (new.id, new.key, new.value, new.tags);
		END`,
		`CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
			DELETE FROM memories_fts WHERE rowid = old.id;
		END`,
		`CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
			DELETE FROM memories_fts WHERE rowid = old.id;
			INSERT INTO memories_fts(rowid, key, value, tags)
			VALUES (new.id, new.key, new.value, new.tags);
		END`,
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, stmt := range steps {
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			head := stmt
			if len(head) > 60 {
				head = head[:60] + "…"
			}
			return fmt.Errorf("step %q: %w", head, err)
		}
	}
	return tx.Commit()
}

func tableHasColumns(db *sql.DB, table string, want ...string) (bool, bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, false, err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	if len(want) != 2 {
		return false, false, fmt.Errorf("tableHasColumns expects exactly 2 columns to check")
	}
	return have[want[0]], have[want[1]], nil
}

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

// resolveScope normalizes scope: "" → DefaultScope for save/get/delete paths.
// List/Search use empty-as-all directly and don't go through this helper.
func resolveScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return DefaultScope
	}
	return scope
}

func (s *Store) Save(ctx context.Context, scope, key, value, tags string) (*Memory, error) {
	scope = resolveScope(scope)
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memories (scope, key, value, tags, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, key) DO UPDATE SET
			value = excluded.value,
			tags = excluded.tags,
			updated_at = excluded.updated_at
	`, scope, key, value, tags, now, now)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, scope, key)
}

func (s *Store) Get(ctx context.Context, scope, key string) (*Memory, error) {
	scope = resolveScope(scope)
	row := s.db.QueryRowContext(ctx, `
		SELECT id, scope, key, value, tags, pinned, created_at, updated_at
		FROM memories
		WHERE scope = ? AND key = ?
	`, scope, key)
	return scanMemory(row)
}

func (s *Store) Delete(ctx context.Context, scope, key string) (bool, error) {
	scope = resolveScope(scope)
	res, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE scope = ? AND key = ?`, scope, key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Pin sets the pinned flag on a memory. Returns the updated record.
func (s *Store) Pin(ctx context.Context, scope, key string, pinned bool) (*Memory, error) {
	scope = resolveScope(scope)
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE memories SET pinned = ?, updated_at = ?
		WHERE scope = ? AND key = ?
	`, boolToInt(pinned), now, scope, key)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, scope, key)
}

// List returns memories sorted by pinned DESC, updated_at DESC. An empty
// scope means "all scopes".
func (s *Store) List(ctx context.Context, scope string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scope, key, value, tags, pinned, created_at, updated_at
		FROM memories
		WHERE (?1 = '' OR scope = ?1)
		ORDER BY pinned DESC, updated_at DESC, id DESC
		LIMIT ?2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// ListPinned is List filtered to pinned=1. Equivalent to a list query with a
// scope filter plus a pinned filter; offered as a separate method because
// the MCP resources/list path needs it on the hot path.
func (s *Store) ListPinned(ctx context.Context, scope string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scope, key, value, tags, pinned, created_at, updated_at
		FROM memories
		WHERE pinned = 1
		  AND (?1 = '' OR scope = ?1)
		ORDER BY updated_at DESC, id DESC
		LIMIT ?2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// escapeLike escapes the LIKE wildcards so user input is treated literally.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ftsQuery turns a free-form user query into an FTS5 MATCH expression.
func ftsQuery(raw string) string {
	parts := strings.Fields(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if isAlphaNum(p) {
			out = append(out, p+"*")
			continue
		}
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

// Search returns ranked, snippet-bearing hits without the full value. Empty
// scope means "search all scopes".
func (s *Store) Search(ctx context.Context, scope, query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(query) == "" {
		return []SearchHit{}, nil
	}
	if expr := ftsQuery(query); expr != "" {
		if out, err := s.searchFTSCompact(ctx, scope, expr, limit); err == nil {
			return out, nil
		}
	}
	return s.searchLikeCompact(ctx, scope, query, limit)
}

// SearchExpanded returns the same ranking as Search but with full Memory
// records (value included). Empty scope = all scopes.
func (s *Store) SearchExpanded(ctx context.Context, scope, query string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(query) == "" {
		return []Memory{}, nil
	}
	if expr := ftsQuery(query); expr != "" {
		if out, err := s.searchFTSExpanded(ctx, scope, expr, limit); err == nil {
			return out, nil
		}
	}
	return s.searchLikeExpanded(ctx, scope, query, limit)
}

func (s *Store) searchFTSCompact(ctx context.Context, scope, expr string, limit int) ([]SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.scope, m.key, m.tags, m.pinned,
			-bm25(memories_fts) AS score,
			snippet(memories_fts, -1, '<<', '>>', '...', 12) AS snip,
			length(CAST(m.value AS BLOB)) AS size_bytes,
			m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?1
		  AND (?2 = '' OR m.scope = ?2)
		ORDER BY f.rank, m.updated_at DESC, m.id DESC
		LIMIT ?3
	`, expr, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		var pinnedInt int
		if err := rows.Scan(&h.ID, &h.Scope, &h.Key, &h.Tags, &pinnedInt, &h.Score, &h.Snippet, &h.SizeBytes, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Pinned = pinnedInt != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) searchFTSExpanded(ctx context.Context, scope, expr string, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.scope, m.key, m.value, m.tags, m.pinned, m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?1
		  AND (?2 = '' OR m.scope = ?2)
		ORDER BY f.rank, m.updated_at DESC, m.id DESC
		LIMIT ?3
	`, expr, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *Store) searchLikeCompact(ctx context.Context, scope, query string, limit int) ([]SearchHit, error) {
	full, err := s.searchLikeExpanded(ctx, scope, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, len(full))
	for i, m := range full {
		out[i] = SearchHit{
			ID:        m.ID,
			Scope:     m.Scope,
			Key:       m.Key,
			Tags:      m.Tags,
			Pinned:    m.Pinned,
			Score:     0,
			Snippet:   makeLikeSnippet(query, m, 100),
			SizeBytes: len(m.Value),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		}
	}
	return out, nil
}

func (s *Store) searchLikeExpanded(ctx context.Context, scope, query string, limit int) ([]Memory, error) {
	q := "%" + strings.ToLower(escapeLike(query)) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scope, key, value, tags, pinned, created_at, updated_at
		FROM memories
		WHERE (LOWER(key) LIKE ?1 ESCAPE '\'
		   OR LOWER(value) LIKE ?1 ESCAPE '\'
		   OR LOWER(tags) LIKE ?1 ESCAPE '\')
		  AND (?2 = '' OR scope = ?2)
		ORDER BY updated_at DESC, id DESC
		LIMIT ?3
	`, q, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// makeLikeSnippet produces an FTS-like snippet for the LIKE fallback path.
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

// scanMemory works for QueryRow*-style scans.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemory(row rowScanner) (*Memory, error) {
	var m Memory
	var pinnedInt int
	if err := row.Scan(&m.ID, &m.Scope, &m.Key, &m.Value, &m.Tags, &pinnedInt, &m.CreatedAt, &m.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Pinned = pinnedInt != 0
	return &m, nil
}

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	out := []Memory{}
	for rows.Next() {
		var m Memory
		var pinnedInt int
		if err := rows.Scan(&m.ID, &m.Scope, &m.Key, &m.Value, &m.Tags, &pinnedInt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		m.Pinned = pinnedInt != 0
		out = append(out, m)
	}
	return out, rows.Err()
}
