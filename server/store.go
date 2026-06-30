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
	ID         int64  `json:"id"`
	Scope      string `json:"scope"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Tags       string `json:"tags"`
	Pinned     bool   `json:"pinned"`
	Archived   bool   `json:"archived"`
	Origin     string `json:"origin,omitempty"`
	VerifiedAt int64  `json:"verified_at"` // 0 means "never verified"
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// SearchHit is the compact result of a search: enough metadata for the
// caller to decide whether to load the full record, plus a contextual
// snippet so the value is usually unnecessary.
type SearchHit struct {
	ID         int64   `json:"id"`
	Scope      string  `json:"scope"`
	Key        string  `json:"key"`
	Tags       string  `json:"tags"`
	Pinned     bool    `json:"pinned"`
	Archived   bool    `json:"archived"`
	Origin     string  `json:"origin,omitempty"`
	VerifiedAt int64   `json:"verified_at"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet"`
	SizeBytes  int     `json:"size_bytes"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
}

type Store struct {
	db *sql.DB
	// embed is optional. When non-nil and not Disabled(), Save auto-embeds
	// the value after the SQL insert/update completes. A nil or disabled
	// client is fine — the row is saved without an embedding and is
	// searchable via FTS only (until `ltm reembed` backfills it).
	embed *EmbedClient
}

// SetEmbedClient attaches (or detaches, if c is nil or disabled) the
// embedding client used by Save. Call once after opening the store; tests
// pass a mock client pointed at httptest.
func (s *Store) SetEmbedClient(c *EmbedClient) {
	s.embed = c
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
	s, err := openStoreAt(path)
	if err != nil {
		return nil, err
	}
	// Production wiring: pick up an OpenAI-compatible embeddings endpoint
	// from the environment. Tests use openStoreAt directly and stay
	// embed-free unless they call SetEmbedClient explicitly.
	s.SetEmbedClient(NewEmbedClientFromEnv())
	return s, nil
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
	//    `embedding` is BLOB NULL by design: rows can exist without an
	//    embedding (endpoint down at save time, pre-0.6 rows awaiting
	//    backfill). `embedding_model` records which model produced the
	//    BLOB so a later model swap can detect dim/version mismatches.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			scope           TEXT NOT NULL DEFAULT 'user',
			key             TEXT NOT NULL,
			value           TEXT NOT NULL,
			tags            TEXT NOT NULL DEFAULT '',
			pinned          INTEGER NOT NULL DEFAULT 0,
			archived        INTEGER NOT NULL DEFAULT 0,
			origin          TEXT NOT NULL DEFAULT '',
			verified_at     INTEGER NOT NULL DEFAULT 0,
			embedding       BLOB,
			embedding_model TEXT NOT NULL DEFAULT '',
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL,
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

	// 3. Now safe: indices on `pinned`, FTS triggers, and the graph layer.
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
	` + linksSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("post-migration schema: %w", err)
	}

	if err := backfillFTS(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("fts backfill: %w", err)
	}

	return &Store{db: db}, nil
}

// migrateSchema upgrades pre-0.2 / pre-0.5 databases to the current schema.
// Idempotent: a no-op once the columns are already in place. The 0.5
// additive migration is wrapped in a transaction so a crash midway leaves
// the schema in a consistent (pre-migration) state. The 0.2 migration is
// already transactional inside migrateTo02.
func migrateSchema(db *sql.DB) error {
	cols, err := columnSet(db, "memories")
	if err != nil {
		return err
	}

	// 0.2 migration: scope+pinned. Requires UNIQUE constraint change so we
	// rebuild the table (own transaction inside migrateTo02).
	if !(cols["scope"] && cols["pinned"]) {
		if err := migrateTo02(db); err != nil {
			return err
		}
		cols, err = columnSet(db, "memories")
		if err != nil {
			return err
		}
	}

	// 0.5 additive migration: archived + origin + verified_at.
	// 0.6 additive migration: embedding (BLOB nullable) + embedding_model.
	// Both batches use plain ALTER TABLE ADD COLUMN — no rebuild because no
	// UNIQUE constraint changes. Adds are wrapped in a single transaction
	// below so a crash midway leaves the schema in a consistent
	// (pre-migration) state.
	type addCol struct{ name, ddl string }
	adds := []addCol{
		{"archived", "ALTER TABLE memories ADD COLUMN archived INTEGER NOT NULL DEFAULT 0"},
		{"origin", "ALTER TABLE memories ADD COLUMN origin TEXT NOT NULL DEFAULT ''"},
		{"verified_at", "ALTER TABLE memories ADD COLUMN verified_at INTEGER NOT NULL DEFAULT 0"},
		{"embedding", "ALTER TABLE memories ADD COLUMN embedding BLOB"},
		{"embedding_model", "ALTER TABLE memories ADD COLUMN embedding_model TEXT NOT NULL DEFAULT ''"},
	}
	pending := adds[:0]
	for _, a := range adds {
		if !cols[a.name] {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, a := range pending {
		if _, err := tx.Exec(a.ddl); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("add column %q: %w", a.name, err)
		}
	}
	return tx.Commit()
}

func columnSet(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		have[name] = true
	}
	return have, rows.Err()
}

func migrateTo02(db *sql.DB) error {

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

// backfillFTS keeps memories_fts in sync with the source table. Two failure
// modes can leave them out of sync:
//   1. memories has rows the FTS index doesn't know about (a pre-FTS DB
//      that was migrated, or rows inserted while triggers were absent).
//   2. memories_fts has rowids that no longer exist in memories (this should
//      not happen under normal trigger flow, but a corrupt DB can produce
//      this and a naive COUNT-based check would miss case 1 forever).
//
// We address both: insert what's missing in FTS, then delete FTS rows whose
// rowid has no match in memories.
func backfillFTS(db *sql.DB) error {
	if _, err := db.Exec(`
		INSERT INTO memories_fts(rowid, key, value, tags)
		SELECT m.id, m.key, m.value, m.tags
		FROM memories m
		WHERE NOT EXISTS (SELECT 1 FROM memories_fts f WHERE f.rowid = m.id)
	`); err != nil {
		return fmt.Errorf("fts insert missing: %w", err)
	}
	if _, err := db.Exec(`
		DELETE FROM memories_fts
		WHERE rowid NOT IN (SELECT id FROM memories)
	`); err != nil {
		return fmt.Errorf("fts delete orphans: %w", err)
	}
	return nil
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

// SaveOptions carries optional fields for Save. Pass nil for the legacy
// no-extras call. Origin is a free-form provenance hint; origin_agent is
// who wrote the memory ("claude-code", "claude-desktop", "cursor", "cli", …).
type SaveOptions struct {
	Origin      string
	OriginAgent string
}

func (s *Store) Save(ctx context.Context, scope, key, value, tags string) (*Memory, error) {
	return s.SaveWithOptions(ctx, scope, key, value, tags, nil)
}

// SaveWithOptions is Save with optional provenance metadata. When opts.Origin
// or opts.OriginAgent is non-empty the stored origin field becomes
// "<origin_agent>: <origin>" / one of the two halves; existing origin is
// preserved on UPDATE if both incoming values are empty.
func (s *Store) SaveWithOptions(ctx context.Context, scope, key, value, tags string, opts *SaveOptions) (*Memory, error) {
	scope = resolveScope(scope)
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	// Conflict resolution check (enabled by default unless LTM_CONFLICT_DISABLE=1)
	// We only run conflict detection if the key being saved does not already exist
	// (either active or archived) in the database. Overwriting same key is a regular update.
	if os.Getenv("LTM_CONFLICT_DISABLE") != "1" {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM memories WHERE scope = ? AND key = ?`, scope, key).Scan(&exists)
		if err == sql.ErrNoRows {
			var newVec []float32
			if s.embed != nil && !s.embed.Disabled() {
				var embedErr error
				newVec, embedErr = s.embed.Embed(ctx, value)
				if embedErr != nil {
					fmt.Fprintf(os.Stderr, "ltm: conflict pre-save embed: %v\n", embedErr)
				}
			}

			conflictKey, err := s.detectConflict(ctx, scope, key, value, tags, newVec)
			if err == nil && conflictKey != "" {
				debugf("ltm: auto-conflict-resolution: new key %q conflicts with old key %q. Running supersede...", key, conflictKey)
				m, err := s.Supersede(ctx, scope, conflictKey, key, value, tags)
				if err != nil {
					return nil, err
				}

				// If we already generated the vector, persist it to avoid double embed calls
				if len(newVec) > 0 && s.embed != nil && !s.embed.Disabled() {
					_ = s.SetEmbedding(ctx, scope, key, newVec, s.embed.Model())
				}

				return m, nil
			}
		}
	}

	now := time.Now().UnixMilli()
	origin := ""
	if opts != nil {
		switch {
		case opts.OriginAgent != "" && opts.Origin != "":
			origin = opts.OriginAgent + ": " + opts.Origin
		case opts.OriginAgent != "":
			origin = opts.OriginAgent
		case opts.Origin != "":
			origin = opts.Origin
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memories (scope, key, value, tags, origin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, key) DO UPDATE SET
			value = excluded.value,
			tags = excluded.tags,
			origin = CASE WHEN excluded.origin <> '' THEN excluded.origin ELSE memories.origin END,
			updated_at = excluded.updated_at
	`, scope, key, value, tags, origin, now, now)
	if err != nil {
		return nil, err
	}
	// Best-effort embedding. Failure is silent (logged to stderr) — the
	// row is saved and FTS-searchable; it just lacks a vector until
	// `ltm reembed` or the next save with a working endpoint.
	s.maybeEmbedAfterSave(ctx, scope, key, value)
	return s.Get(ctx, scope, key)
}

// detectConflict scans existing active memories in the scope for semantic or keyword overlap.
func (s *Store) detectConflict(ctx context.Context, scope, key, value, tags string, newVec []float32) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value, tags, embedding 
		FROM memories 
		WHERE scope = ? AND key <> ? AND archived = 0
	`, scope, key)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var conflictKey string
	maxSimilarity := 0.0

	const vectorThreshold = 0.85
	const jaccardThreshold = 0.70

	for rows.Next() {
		var oldKey, oldValue, oldTags string
		var blob []byte
		if err := rows.Scan(&oldKey, &oldValue, &oldTags, &blob); err != nil {
			continue
		}

		// Try vector similarity if we have embeddings for both
		if len(newVec) > 0 && len(blob) > 0 {
			oldVec, err := decodeEmbedding(blob)
			if err == nil && len(oldVec) == len(newVec) {
				sim := cosine(newVec, oldVec)
				if sim > vectorThreshold && sim > maxSimilarity {
					maxSimilarity = sim
					conflictKey = oldKey
					continue
				}
			}
		}

		// Fallback: Jaccard overlap on tokenized values
		sim := jaccardSimilarity(value+" "+tags, oldValue+" "+oldTags)
		if sim > jaccardThreshold && sim > maxSimilarity {
			maxSimilarity = sim
			conflictKey = oldKey
		}
	}

	if err := rows.Err(); err != nil {
		return "", err
	}

	if conflictKey != "" {
		return conflictKey, nil
	}

	return "", nil
}

// jaccardSimilarity computes Jaccard similarity index based on token overlap.
func jaccardSimilarity(s1, s2 string) float64 {
	tokens1 := tokenize(s1)
	tokens2 := tokenize(s2)

	if len(tokens1) == 0 || len(tokens2) == 0 {
		return 0
	}

	intersection := 0
	for t := range tokens1 {
		if tokens2[t] {
			intersection++
		}
	}

	union := len(tokens1) + len(tokens2) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// tokenize splits a string into lowercase alphanumeric tokens, filtering stop words.
func tokenize(s string) map[string]bool {
	tokens := make(map[string]bool)
	var current []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, unicode.ToLower(r))
		} else {
			if len(current) > 0 {
				tok := string(current)
				if !isStopWord(tok) {
					tokens[tok] = true
				}
				current = nil
			}
		}
	}
	if len(current) > 0 {
		tok := string(current)
		if !isStopWord(tok) {
			tokens[tok] = true
		}
	}
	return tokens
}

// isStopWord returns true if the token is a common noise word.
func isStopWord(w string) bool {
	switch w {
	case "the", "a", "is", "in", "to", "and", "or", "of", "for", "on", "with", "this", "that", "it", "an", "be", "as", "by", "at",
		"il", "lo", "la", "i", "gli", "le", "di", "da", "con", "su", "per", "tra", "fra", "e", "o", "un", "una", "uno", "che", "del", "al":
		return true
	}
	return false
}

// maybeEmbedAfterSave is the synchronous embed-on-save hook. No-op when
// no client is attached or the client is disabled. Errors are logged to
// stderr and swallowed: the contract of Save is "the row is durably
// stored", not "the row is durably embedded".
func (s *Store) maybeEmbedAfterSave(ctx context.Context, scope, key, value string) {
	if s.embed == nil || s.embed.Disabled() {
		return
	}
	vec, err := s.embed.Embed(ctx, value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ltm: embed save-path %s/%s: %v\n", scope, key, err)
		return
	}
	if err := s.SetEmbedding(ctx, scope, key, vec, s.embed.Model()); err != nil {
		fmt.Fprintf(os.Stderr, "ltm: persist embedding %s/%s: %v\n", scope, key, err)
	}
}

func (s *Store) Get(ctx context.Context, scope, key string) (*Memory, error) {
	scope = resolveScope(scope)
	row := s.db.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM memories WHERE scope = ? AND key = ?`, scope, key)
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

// Rename moves a memory from (scope, oldKey) to (scope, newKey) in a single
// transaction. Incident links are kept (their from/to keys are updated to
// match). Returns ErrNotFound if (scope, oldKey) doesn't exist; returns an
// error if (scope, newKey) is already taken.
func (s *Store) Rename(ctx context.Context, scope, oldKey, newKey string) (*Memory, error) {
	scope = resolveScope(scope)
	oldKey = strings.TrimSpace(oldKey)
	newKey = strings.TrimSpace(newKey)
	if oldKey == "" || newKey == "" {
		return nil, fmt.Errorf("old and new keys are required")
	}
	if oldKey == newKey {
		return s.Get(ctx, scope, oldKey)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// FK on (memory_links → memories) is composite, so SQLite has no
	// ON UPDATE CASCADE for it. Defer the FK check to commit time so we
	// can rewrite memories.key first and then memory_links.
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return nil, err
	}

	// 1. Source must exist.
	var srcID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM memories WHERE scope = ? AND key = ?`, scope, oldKey,
	).Scan(&srcID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// 2. Destination must be free.
	var dstID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM memories WHERE scope = ? AND key = ?`, scope, newKey,
	).Scan(&dstID)
	if err == nil {
		return nil, fmt.Errorf("memory %q already exists in scope %q", newKey, scope)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`UPDATE memories SET key = ?, updated_at = ? WHERE scope = ? AND key = ?`,
		newKey, now, scope, oldKey,
	); err != nil {
		return nil, err
	}
	// Cascade rename through incident links — composite FK isn't an
	// ON UPDATE CASCADE, so we update both sides explicitly.
	if _, err := tx.ExecContext(ctx,
		`UPDATE memory_links SET from_key = ? WHERE from_scope = ? AND from_key = ?`,
		newKey, scope, oldKey,
	); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE memory_links SET to_key = ? WHERE to_scope = ? AND to_key = ?`,
		newKey, scope, oldKey,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, scope, newKey)
}

// Pin sets the pinned flag on a memory. Returns the updated record.
// Pinning implies a verify (the user has just confirmed this is current).
func (s *Store) Pin(ctx context.Context, scope, key string, pinned bool) (*Memory, error) {
	scope = resolveScope(scope)
	now := time.Now().UnixMilli()
	var res sql.Result
	var err error
	if pinned {
		res, err = s.db.ExecContext(ctx, `
			UPDATE memories SET pinned = 1, verified_at = ?, updated_at = ?
			WHERE scope = ? AND key = ?
		`, now, now, scope, key)
	} else {
		res, err = s.db.ExecContext(ctx, `
			UPDATE memories SET pinned = 0, updated_at = ?
			WHERE scope = ? AND key = ?
		`, now, scope, key)
	}
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, scope, key)
}

// Verify marks a memory as freshly confirmed by the user. The verified_at
// timestamp is used to compute staleness signals in compact views.
func (s *Store) Verify(ctx context.Context, scope, key string) (*Memory, error) {
	scope = resolveScope(scope)
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE memories SET verified_at = ? WHERE scope = ? AND key = ?
	`, now, scope, key)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, scope, key)
}

// Supersede archives an old memory and creates a new one in its place,
// linking new --supersedes--> old. The old memory is kept (archived) so
// historical references stay valid; list/search hide it by default.
func (s *Store) Supersede(ctx context.Context, scope, oldKey, newKey, value, tags string) (*Memory, error) {
	scope = resolveScope(scope)
	oldKey = strings.TrimSpace(oldKey)
	newKey = strings.TrimSpace(newKey)
	if oldKey == "" || newKey == "" {
		return nil, fmt.Errorf("old and new keys are required")
	}
	if oldKey == newKey {
		return nil, fmt.Errorf("supersede requires a new key distinct from the old one")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Old must exist and not already be archived.
	var oldID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM memories WHERE scope = ? AND key = ?`, scope, oldKey,
	).Scan(&oldID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// New must not exist.
	var dst int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM memories WHERE scope = ? AND key = ?`, scope, newKey,
	).Scan(&dst)
	if err == nil {
		return nil, fmt.Errorf("memory %q already exists in scope %q", newKey, scope)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memories (scope, key, value, tags, verified_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, scope, newKey, value, tags, now, now, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE memories SET archived = 1, updated_at = ? WHERE scope = ? AND key = ?
	`, now, scope, oldKey); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO memory_links
			(from_scope, from_key, to_scope, to_key, rel, created_at)
		VALUES (?, ?, ?, ?, 'supersedes', ?)
	`, scope, newKey, scope, oldKey, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, scope, newKey)
}

// List returns active (non-archived) memories sorted by pinned DESC,
// updated_at DESC. An empty scope means "all scopes". Pass includeArchived
// to also surface archived rows (last by default ordering).
func (s *Store) List(ctx context.Context, scope string, limit int) ([]Memory, error) {
	return s.listWithFlags(ctx, scope, limit, false)
}

func (s *Store) ListIncludingArchived(ctx context.Context, scope string, limit int) ([]Memory, error) {
	return s.listWithFlags(ctx, scope, limit, true)
}

func (s *Store) listWithFlags(ctx context.Context, scope string, limit int, includeArchived bool) ([]Memory, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+memoryColumns+`
		FROM memories
		WHERE (?1 = '' OR scope = ?1)
		  AND (?2 = 1 OR archived = 0)
		ORDER BY archived ASC, pinned DESC, updated_at DESC, id DESC
		LIMIT ?3
	`, scope, boolToInt(includeArchived), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// ListPinned is List filtered to pinned=1. Pinned memories are never
// archived (a successor never inherits the pin), so this query implicitly
// excludes archived rows.
func (s *Store) ListPinned(ctx context.Context, scope string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+memoryColumns+`
		FROM memories
		WHERE pinned = 1 AND archived = 0
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
//
// When an embedding client is attached (and the query embedding succeeds)
// the result is the RRF blend of FTS and vector hits — this is what makes
// cross-lingual queries find their target (an Italian conceptual query
// against an English-bodied memory). Otherwise the path is pure FTS, with
// a LIKE fallback for queries that defeat the FTS5 parser.
//
// Failure modes that fall back to FTS-only:
//   - No embed client attached.
//   - Client is Disabled (LTM_EMBED_DISABLE=1 or empty URL).
//   - Query embedding errors (endpoint down, timeout, decode failure).
//   - VectorSearch errors (DB-level).
// Each is logged once to stderr; the search itself never errors out.
func (s *Store) Search(ctx context.Context, scope, query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(query) == "" {
		return []SearchHit{}, nil
	}

	// FTS is always attempted; it's the back-compat baseline.
	var ftsHits []SearchHit
	if expr := ftsQuery(query); expr != "" {
		hits, err := s.searchFTSCompact(ctx, scope, expr, limit*3)
		if err == nil {
			ftsHits = hits
		}
	}
	if ftsHits == nil {
		hits, err := s.searchLikeCompact(ctx, scope, query, limit*3)
		if err != nil {
			return nil, err
		}
		ftsHits = hits
	}

	// Try the vector path. Any failure short-circuits to FTS-only result.
	if s.embed == nil || s.embed.Disabled() {
		return capHits(ftsHits, limit), nil
	}
	qvec, err := s.embed.Embed(ctx, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ltm: search embed-query: %v (falling back to FTS)\n", err)
		return capHits(ftsHits, limit), nil
	}
	vecHits, err := s.VectorSearch(ctx, scope, qvec, limit*3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ltm: vector search: %v (falling back to FTS)\n", err)
		return capHits(ftsHits, limit), nil
	}
	return rrfBlend(ftsHits, vecHits, 60, limit), nil
}

// capHits truncates a slice to at most `limit` entries; helper to keep the
// FTS-only paths consistent with the hybrid path's limit semantics.
func capHits(hits []SearchHit, limit int) []SearchHit {
	if len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

// SearchExpanded returns the same ranking as Search but with full Memory
// records (value included). Empty scope = all scopes.
//
// Implementation: re-uses Search to compute the hybrid ranking, then
// backfills value via a single batched IN-clause SELECT. The N+1 cost is
// avoided; for limit=50 we do exactly two DB queries (or three if we hit
// the LIKE fallback inside Search).
func (s *Store) SearchExpanded(ctx context.Context, scope, query string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	if strings.TrimSpace(query) == "" {
		return []Memory{}, nil
	}
	hits, err := s.Search(ctx, scope, query, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return []Memory{}, nil
	}

	// Batched value lookup. Build (?, ?, ?) placeholders and pull values
	// for the surviving hit ids in a single roundtrip.
	ids := make([]any, len(hits))
	placeholders := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
		placeholders[i] = "?"
	}
	q := `SELECT id, value FROM memories WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := s.db.QueryContext(ctx, q, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	valueByID := make(map[int64]string, len(hits))
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		valueByID[id] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Memory, 0, len(hits))
	for _, h := range hits {
		out = append(out, Memory{
			ID:         h.ID,
			Scope:      h.Scope,
			Key:        h.Key,
			Value:      valueByID[h.ID],
			Tags:       h.Tags,
			Pinned:     h.Pinned,
			Archived:   h.Archived,
			Origin:     h.Origin,
			VerifiedAt: h.VerifiedAt,
			CreatedAt:  h.CreatedAt,
			UpdatedAt:  h.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) searchFTSCompact(ctx context.Context, scope, expr string, limit int) ([]SearchHit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.scope, m.key, m.tags, m.pinned, m.archived, m.origin, m.verified_at,
			-bm25(memories_fts) AS score,
			snippet(memories_fts, -1, '<<', '>>', '...', 12) AS snip,
			length(CAST(m.value AS BLOB)) AS size_bytes,
			m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?1
		  AND (?2 = '' OR m.scope = ?2)
		  AND m.archived = 0
		ORDER BY m.pinned DESC, f.rank, m.updated_at DESC, m.id DESC
		LIMIT ?3
	`, expr, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		var pinnedInt, archivedInt int
		if err := rows.Scan(&h.ID, &h.Scope, &h.Key, &h.Tags, &pinnedInt, &archivedInt, &h.Origin, &h.VerifiedAt,
			&h.Score, &h.Snippet, &h.SizeBytes, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.Pinned = pinnedInt != 0
		h.Archived = archivedInt != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) searchFTSExpanded(ctx context.Context, scope, expr string, limit int) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixColumns("m", memoryColumns)+`
		FROM memories_fts f
		JOIN memories m ON m.id = f.rowid
		WHERE memories_fts MATCH ?1
		  AND (?2 = '' OR m.scope = ?2)
		  AND m.archived = 0
		ORDER BY m.pinned DESC, f.rank, m.updated_at DESC, m.id DESC
		LIMIT ?3
	`, expr, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// prefixColumns turns "id, scope, key" into "m.id, m.scope, m.key".
func prefixColumns(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = " " + alias + "." + strings.TrimSpace(p)
	}
	return strings.TrimLeft(strings.Join(parts, ","), " ")
}

func (s *Store) searchLikeCompact(ctx context.Context, scope, query string, limit int) ([]SearchHit, error) {
	full, err := s.searchLikeExpanded(ctx, scope, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, len(full))
	for i, m := range full {
		out[i] = SearchHit{
			ID:         m.ID,
			Scope:      m.Scope,
			Key:        m.Key,
			Tags:       m.Tags,
			Pinned:     m.Pinned,
			Archived:   m.Archived,
			Origin:     m.Origin,
			VerifiedAt: m.VerifiedAt,
			Score:      0,
			Snippet:    makeLikeSnippet(query, m, 100),
			SizeBytes:  len(m.Value),
			CreatedAt:  m.CreatedAt,
			UpdatedAt:  m.UpdatedAt,
		}
	}
	return out, nil
}

func (s *Store) searchLikeExpanded(ctx context.Context, scope, query string, limit int) ([]Memory, error) {
	q := "%" + strings.ToLower(escapeLike(query)) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+memoryColumns+`
		FROM memories
		WHERE (LOWER(key) LIKE ?1 ESCAPE '\'
		   OR LOWER(value) LIKE ?1 ESCAPE '\'
		   OR LOWER(tags) LIKE ?1 ESCAPE '\')
		  AND (?2 = '' OR scope = ?2)
		  AND archived = 0
		ORDER BY pinned DESC, updated_at DESC, id DESC
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

// memoryColumns is the canonical column list for SELECT memories.
const memoryColumns = "id, scope, key, value, tags, pinned, archived, origin, verified_at, created_at, updated_at"

// scanMemory works for QueryRow*-style scans.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemory(row rowScanner) (*Memory, error) {
	var m Memory
	var pinnedInt, archivedInt int
	if err := row.Scan(&m.ID, &m.Scope, &m.Key, &m.Value, &m.Tags,
		&pinnedInt, &archivedInt, &m.Origin, &m.VerifiedAt,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.Pinned = pinnedInt != 0
	m.Archived = archivedInt != 0
	return &m, nil
}

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	out := []Memory{}
	for rows.Next() {
		var m Memory
		var pinnedInt, archivedInt int
		if err := rows.Scan(&m.ID, &m.Scope, &m.Key, &m.Value, &m.Tags,
			&pinnedInt, &archivedInt, &m.Origin, &m.VerifiedAt,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		m.Pinned = pinnedInt != 0
		m.Archived = archivedInt != 0
		out = append(out, m)
	}
	return out, rows.Err()
}
