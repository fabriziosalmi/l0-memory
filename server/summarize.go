package main

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// CompactView builds a token-efficient descriptor of a memory: enough
// metadata for an LLM to decide whether it actually needs to load the full
// value, but never the value itself. Use Memory.toFull() (or memory_get with
// expand:true) when the caller really wants the body.
func CompactView(m *Memory) map[string]any {
	out := map[string]any{
		"scope":      m.Scope,
		"key":        m.Key,
		"tags":       m.Tags,
		"pinned":     m.Pinned,
		"archived":   m.Archived,
		"created_at": m.CreatedAt,
		"updated_at": m.UpdatedAt,
		"size_bytes": len(m.Value),
		"compact":    true,
	}
	if m.Origin != "" {
		out["origin"] = m.Origin
	}
	if m.VerifiedAt > 0 {
		out["verified_at"] = m.VerifiedAt
		// staleness_days: how long since the user last said "this is still
		// current". Hosts can use it to decide whether to attach the memory
		// to context unconditionally or to flag it.
		ageMs := time.Now().UnixMilli() - m.VerifiedAt
		out["staleness_days"] = ageMs / (1000 * 60 * 60 * 24)
	}
	if json.Valid([]byte(m.Value)) && looksLikeJSONStructure(m.Value) {
		out["is_json"] = true
		out["schema"] = summarizeJSON(m.Value)
		out["hint"] = "value omitted; use memory_query to read slices or memory_get with expand:true for the full value"
	} else {
		out["is_json"] = false
		out["preview"] = preview(m.Value, 240)
		out["hint"] = "value truncated; use memory_get with expand:true for the full text"
	}
	return out
}

// looksLikeJSONStructure rejects bare scalars (a single number, string, or
// bool), which json.Valid happily accepts but that aren't useful to summarise
// as a "schema". For those we prefer the textual preview path.
func looksLikeJSONStructure(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	c := t[0]
	return c == '{' || c == '['
}

// summarizeJSON returns a small descriptor of a JSON value: top-level keys
// (for objects), length (for arrays), or the scalar's type. Deliberately
// shallow — going deeper would defeat the point of a compact view.
func summarizeJSON(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]any{
			"type":     "object",
			"key_count": len(keys),
			"keys":     keys,
		}
	case []any:
		return map[string]any{
			"type": "array",
			"len":  len(x),
		}
	default:
		return map[string]any{"type": "scalar"}
	}
}

// preview returns up to n bytes of s, trimmed to a word boundary when
// possible. Output is always valid UTF-8 (no half-runes) and clipped with an
// ellipsis when truncation occurs.
func preview(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// First, take n bytes, then back off to the nearest valid rune boundary.
	cut := s[:n]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	// Then prefer a word/whitespace boundary if one exists in the second half.
	if i := strings.LastIndexAny(cut, " \n\t,;.:!?"); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n\t,;.:!?") + "…"
}
