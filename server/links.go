package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Link is a directed, typed edge between two memories. Rel is a free-form
// label like "depends_on", "implements", "see_also". The (from, to, rel)
// triple is unique — a second Link call with the same triple is a no-op.
type Link struct {
	ID        int64  `json:"id"`
	FromScope string `json:"from_scope"`
	FromKey   string `json:"from_key"`
	ToScope   string `json:"to_scope"`
	ToKey     string `json:"to_key"`
	Rel       string `json:"rel"`
	CreatedAt int64  `json:"created_at"`
}

// LinkedMemory is a Memory plus the relationship and direction by which it
// was reached from a starting node. Direction is "out" (the memory is the
// to side of the link) or "in" (it is the from side).
type LinkedMemory struct {
	Memory    Memory `json:"memory"`
	Rel       string `json:"rel"`
	Direction string `json:"direction"`
}

// GraphView is the structured result of Traverse. Hosts can render it as a
// graph; LLMs can use it to reason about the local neighbourhood.
type GraphView struct {
	Root  string      `json:"root"`
	Depth int         `json:"depth"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphNode struct {
	URI    string `json:"uri"`
	Scope  string `json:"scope"`
	Key    string `json:"key"`
	Tags   string `json:"tags"`
	Pinned bool   `json:"pinned"`
	Depth  int    `json:"depth"`
}

type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel"`
}

// linksSchema is appended to openStoreAt's post-migration block. It uses
// composite foreign keys so deleting a memory cascades to its incident
// links — relies on the foreign_keys pragma being on (it is, in
// openStoreAt's connection string).
const linksSchema = `
CREATE TABLE IF NOT EXISTS memory_links (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	from_scope   TEXT NOT NULL,
	from_key     TEXT NOT NULL,
	to_scope     TEXT NOT NULL,
	to_key       TEXT NOT NULL,
	rel          TEXT NOT NULL,
	created_at   INTEGER NOT NULL,
	UNIQUE(from_scope, from_key, to_scope, to_key, rel),
	FOREIGN KEY(from_scope, from_key) REFERENCES memories(scope, key) ON DELETE CASCADE,
	FOREIGN KEY(to_scope, to_key)     REFERENCES memories(scope, key) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_links_from ON memory_links(from_scope, from_key);
CREATE INDEX IF NOT EXISTS idx_links_to   ON memory_links(to_scope, to_key);
`

func validateRel(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return errors.New("'rel' is required")
	}
	return nil
}

// Link creates a (from → to, rel) edge. Both endpoints must exist; rel is
// required. Re-linking the same triple is a no-op (returns the existing
// row).
func (s *Store) Link(ctx context.Context, fromScope, fromKey, toScope, toKey, rel string) (*Link, error) {
	fromScope = resolveScope(fromScope)
	toScope = resolveScope(toScope)
	fromKey = strings.TrimSpace(fromKey)
	toKey = strings.TrimSpace(toKey)
	if fromKey == "" || toKey == "" {
		return nil, errors.New("from_key and to_key are required")
	}
	if err := validateRel(rel); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO memory_links
			(from_scope, from_key, to_scope, to_key, rel, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, fromScope, fromKey, toScope, toKey, rel, now)
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, from_scope, from_key, to_scope, to_key, rel, created_at
		FROM memory_links
		WHERE from_scope=? AND from_key=? AND to_scope=? AND to_key=? AND rel=?
	`, fromScope, fromKey, toScope, toKey, rel)
	var l Link
	if err := row.Scan(&l.ID, &l.FromScope, &l.FromKey, &l.ToScope, &l.ToKey, &l.Rel, &l.CreatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

// Unlink removes a single (from, to, rel) edge. Returns true if a row was
// deleted, false if no matching link existed.
func (s *Store) Unlink(ctx context.Context, fromScope, fromKey, toScope, toKey, rel string) (bool, error) {
	fromScope = resolveScope(fromScope)
	toScope = resolveScope(toScope)
	if err := validateRel(rel); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM memory_links
		WHERE from_scope=? AND from_key=? AND to_scope=? AND to_key=? AND rel=?
	`, fromScope, fromKey, toScope, toKey, rel)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Links returns every link incident to (scope, key), in either direction.
// Useful for "what is X connected to?" queries.
func (s *Store) Links(ctx context.Context, scope, key string) ([]Link, error) {
	scope = resolveScope(scope)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_scope, from_key, to_scope, to_key, rel, created_at
		FROM memory_links
		WHERE (from_scope=? AND from_key=?) OR (to_scope=? AND to_key=?)
		ORDER BY created_at DESC, id DESC
	`, scope, key, scope, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.FromScope, &l.FromKey, &l.ToScope, &l.ToKey, &l.Rel, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Direction filter for Neighbors / Traverse.
const (
	DirOut  = "out"
	DirIn   = "in"
	DirBoth = "both"
)

func normalizeDirection(d string) (string, error) {
	switch d {
	case "", DirBoth:
		return DirBoth, nil
	case DirOut, DirIn:
		return d, nil
	default:
		return "", fmt.Errorf("invalid direction %q (want out|in|both)", d)
	}
}

// Neighbors returns the memories one hop away from (scope, key), with the
// link rel and direction by which they were reached. `rel` filters edges by
// label (empty = any). `direction` is "out", "in" or "both".
func (s *Store) Neighbors(ctx context.Context, scope, key, rel, direction string) ([]LinkedMemory, error) {
	scope = resolveScope(scope)
	dir, err := normalizeDirection(direction)
	if err != nil {
		return nil, err
	}
	relFilter := strings.TrimSpace(rel)
	args := []any{scope, key, relFilter, relFilter}
	q := `
		SELECT 'out' AS direction, l.rel, m.id, m.scope, m.key, m.value, m.tags, m.pinned, m.created_at, m.updated_at
		FROM memory_links l
		JOIN memories m ON m.scope = l.to_scope AND m.key = l.to_key
		WHERE l.from_scope = ?1 AND l.from_key = ?2
		  AND (?3 = '' OR l.rel = ?4)
	`
	if dir == DirBoth || dir == DirIn {
		q = `
		SELECT 'in' AS direction, l.rel, m.id, m.scope, m.key, m.value, m.tags, m.pinned, m.created_at, m.updated_at
		FROM memory_links l
		JOIN memories m ON m.scope = l.from_scope AND m.key = l.from_key
		WHERE l.to_scope = ?1 AND l.to_key = ?2
		  AND (?3 = '' OR l.rel = ?4)
		` + ifThen(dir == DirBoth, " UNION ALL "+`
		SELECT 'out' AS direction, l.rel, m.id, m.scope, m.key, m.value, m.tags, m.pinned, m.created_at, m.updated_at
		FROM memory_links l
		JOIN memories m ON m.scope = l.to_scope AND m.key = l.to_key
		WHERE l.from_scope = ?1 AND l.from_key = ?2
		  AND (?3 = '' OR l.rel = ?4)
		`, "")
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkedMemory{}
	for rows.Next() {
		var lm LinkedMemory
		var pinnedInt int
		if err := rows.Scan(
			&lm.Direction, &lm.Rel,
			&lm.Memory.ID, &lm.Memory.Scope, &lm.Memory.Key, &lm.Memory.Value,
			&lm.Memory.Tags, &pinnedInt, &lm.Memory.CreatedAt, &lm.Memory.UpdatedAt,
		); err != nil {
			return nil, err
		}
		lm.Memory.Pinned = pinnedInt != 0
		out = append(out, lm)
	}
	return out, rows.Err()
}

func ifThen(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

// Traverse runs a breadth-first walk from (scope, key) and returns the
// reached subgraph compacted into nodes + edges. depth ≤ 0 falls back to 1.
// rel filters edges by label (empty = any). direction is out|in|both.
func (s *Store) Traverse(ctx context.Context, scope, key string, depth int, rel, direction string) (*GraphView, error) {
	scope = resolveScope(scope)
	if depth <= 0 {
		depth = 1
	}
	dir, err := normalizeDirection(direction)
	if err != nil {
		return nil, err
	}
	root, err := s.Get(ctx, scope, key)
	if err != nil {
		return nil, err
	}

	view := &GraphView{
		Root:  makeMemoryURI(root.Scope, root.Key),
		Depth: depth,
	}
	seen := map[string]int{} // uri -> depth
	addNode := func(m *Memory, d int) {
		uri := makeMemoryURI(m.Scope, m.Key)
		if _, ok := seen[uri]; ok {
			return
		}
		seen[uri] = d
		view.Nodes = append(view.Nodes, GraphNode{
			URI:    uri,
			Scope:  m.Scope,
			Key:    m.Key,
			Tags:   m.Tags,
			Pinned: m.Pinned,
			Depth:  d,
		})
	}
	addNode(root, 0)

	// seenEdge dedups (from, to, rel) triples. With direction=both, an edge
	// A→B is reported by Neighbors(A) as DirOut and again by Neighbors(B) as
	// DirIn during the BFS, which without dedup emits the same edge twice.
	seenEdge := map[string]struct{}{}
	addEdge := func(from, to, rel string) {
		k := from + "\x00" + to + "\x00" + rel
		if _, ok := seenEdge[k]; ok {
			return
		}
		seenEdge[k] = struct{}{}
		view.Edges = append(view.Edges, GraphEdge{From: from, To: to, Rel: rel})
	}

	type frontierItem struct {
		mem   *Memory
		depth int
	}
	frontier := []frontierItem{{root, 0}}

	for len(frontier) > 0 {
		next := []frontierItem{}
		for _, f := range frontier {
			if f.depth >= depth {
				continue
			}
			neigh, err := s.Neighbors(ctx, f.mem.Scope, f.mem.Key, rel, dir)
			if err != nil {
				return nil, err
			}
			fromURI := makeMemoryURI(f.mem.Scope, f.mem.Key)
			for i := range neigh {
				n := neigh[i]
				toURI := makeMemoryURI(n.Memory.Scope, n.Memory.Key)
				switch n.Direction {
				case DirOut:
					addEdge(fromURI, toURI, n.Rel)
				case DirIn:
					addEdge(toURI, fromURI, n.Rel)
				}
				if _, already := seen[toURI]; !already {
					addNode(&n.Memory, f.depth+1)
					next = append(next, frontierItem{&neigh[i].Memory, f.depth + 1})
				}
			}
		}
		frontier = next
	}
	return view, nil
}
