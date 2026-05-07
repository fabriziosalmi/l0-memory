package main

import (
	"errors"
	"reflect"
	"testing"
)

const fixture = `{
	"stats": {"repos": 3, "stars": 100},
	"clusters": {
		"sec":  {"members": ["caddy-waf", "patterns"]},
		"misc": {"members": ["other"]}
	},
	"repos": [
		{"n": "caddy-waf", "s": 60, "u": "https://x/caddy-waf"},
		{"n": "patterns",  "s": 30, "u": "https://x/patterns"},
		{"n": "noUrl",     "s": 10}
	]
}`

func TestEvalJSONPathRoot(t *testing.T) {
	got, err := EvalJSONPath(fixture, "")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected object at root, got %T", got)
	}
	if _, has := m["stats"]; !has {
		t.Fatalf("root should contain 'stats': %+v", m)
	}
	got2, _ := EvalJSONPath(fixture, "/")
	if !reflect.DeepEqual(got, got2) {
		t.Fatalf("'' and '/' should both return whole doc")
	}
}

func TestEvalJSONPathDescend(t *testing.T) {
	got, err := EvalJSONPath(fixture, "/stats/stars")
	if err != nil {
		t.Fatal(err)
	}
	if got != float64(100) {
		t.Fatalf("expected 100, got %v (%T)", got, got)
	}
}

func TestEvalJSONPathArrayIndex(t *testing.T) {
	got, err := EvalJSONPath(fixture, "/repos/1/n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "patterns" {
		t.Fatalf("expected 'patterns', got %v", got)
	}
}

func TestEvalJSONPathWildcardOverArray(t *testing.T) {
	got, err := EvalJSONPath(fixture, "/repos/*/n")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"caddy-waf", "patterns", "noUrl"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEvalJSONPathWildcardSilentlySkipsMissingChildren(t *testing.T) {
	// /repos/*/u: only 2 of the 3 repos have a "u" key; the third is dropped.
	got, err := EvalJSONPath(fixture, "/repos/*/u")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"https://x/caddy-waf", "https://x/patterns"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEvalJSONPathWildcardOverObject(t *testing.T) {
	// Wildcard over an object iterates values in key-sorted order ("misc" < "sec").
	got, err := EvalJSONPath(fixture, "/clusters/*/members/0")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"other", "caddy-waf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEvalJSONPathMissingKey(t *testing.T) {
	_, err := EvalJSONPath(fixture, "/stats/nope")
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("expected ErrPathNotFound, got %v", err)
	}
}

func TestEvalJSONPathOutOfRangeIndex(t *testing.T) {
	_, err := EvalJSONPath(fixture, "/repos/99")
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("expected ErrPathNotFound, got %v", err)
	}
}

func TestEvalJSONPathDescendIntoScalarErrors(t *testing.T) {
	_, err := EvalJSONPath(fixture, "/stats/stars/inner")
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("expected ErrPathNotFound when path goes through a scalar, got %v", err)
	}
}

func TestEvalJSONPathInvalidJSON(t *testing.T) {
	_, err := EvalJSONPath("not json at all", "/x")
	if !errors.Is(err, ErrNotJSON) {
		t.Fatalf("expected ErrNotJSON, got %v", err)
	}
}

func TestEvalJSONPathRequiresLeadingSlash(t *testing.T) {
	_, err := EvalJSONPath(fixture, "stats/stars")
	if err == nil {
		t.Fatal("expected error for path without leading slash")
	}
}

func TestEvalJSONPathRFC6901EscapeSequences(t *testing.T) {
	// "~1" stands for "/", "~0" stands for "~".
	doc := `{"a/b": 1, "x~y": 2}`
	got, err := EvalJSONPath(doc, "/a~1b")
	if err != nil || got != float64(1) {
		t.Fatalf("'~1' escape: got %v err=%v", got, err)
	}
	got, err = EvalJSONPath(doc, "/x~0y")
	if err != nil || got != float64(2) {
		t.Fatalf("'~0' escape: got %v err=%v", got, err)
	}
}
