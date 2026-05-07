package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrPathNotFound is returned when a path cannot be resolved against a JSON
// document (missing key, out-of-range index, type mismatch).
var ErrPathNotFound = errors.New("path not found")

// ErrNotJSON is returned when the memory value is not valid JSON and a
// structured query was requested.
var ErrNotJSON = errors.New("memory value is not valid JSON")

// EvalJSONPath returns the slice of `raw` (a JSON document) selected by `path`.
// The path syntax is JSON Pointer (RFC 6901):
//
//   - Empty string or "/" selects the whole document.
//   - "/foo/bar" descends through object keys.
//   - "/arr/3" indexes into arrays.
//   - "~0" inside a segment escapes "~"; "~1" escapes "/".
//
// On top of RFC 6901 we accept a single extension: a "*" segment fans out
// over the current node. On arrays it iterates every element; on objects it
// iterates values in key-sorted order. The result is an array of whatever
// the rest of the path resolves to. Children where the rest of the path is
// missing are silently dropped, so "/repos/*/u" returns just the URLs of
// repos that have one.
func EvalJSONPath(raw string, path string) (any, error) {
	if !json.Valid([]byte(raw)) {
		return nil, ErrNotJSON
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, ErrNotJSON
	}
	if path == "" || path == "/" {
		return doc, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path must start with '/' or be empty (got %q)", path)
	}
	segments := strings.Split(path[1:], "/")
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for i, seg := range segments {
		segments[i] = unescape.Replace(seg)
	}
	return descend(doc, segments)
}

func descend(node any, segments []string) (any, error) {
	if len(segments) == 0 {
		return node, nil
	}
	seg, rest := segments[0], segments[1:]

	if seg == "*" {
		switch v := node.(type) {
		case []any:
			out := make([]any, 0, len(v))
			for _, item := range v {
				child, err := descend(item, rest)
				if err == nil {
					out = append(out, child)
				}
			}
			return out, nil
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]any, 0, len(keys))
			for _, k := range keys {
				child, err := descend(v[k], rest)
				if err == nil {
					out = append(out, child)
				}
			}
			return out, nil
		default:
			return nil, ErrPathNotFound
		}
	}

	switch v := node.(type) {
	case map[string]any:
		next, ok := v[seg]
		if !ok {
			return nil, ErrPathNotFound
		}
		return descend(next, rest)
	case []any:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(v) {
			return nil, ErrPathNotFound
		}
		return descend(v[idx], rest)
	default:
		// Scalar (string/number/bool/null) but more segments remain.
		return nil, ErrPathNotFound
	}
}
