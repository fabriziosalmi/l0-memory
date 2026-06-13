package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestPreviewShortStringIsReturnedVerbatim(t *testing.T) {
	got := preview("hello", 100)
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestPreviewEmptyString(t *testing.T) {
	got := preview("", 50)
	if got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

func TestPreviewTrimsAndTruncates(t *testing.T) {
	in := "  " + strings.Repeat("a", 500) + "  "
	got := preview(in, 50)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis, got %q", got)
	}
	// 50 bytes plus "…" — never longer.
	if len(got) > 60 {
		t.Errorf("preview too long: %d bytes (%q)", len(got), got)
	}
}

func TestPreviewBreaksOnWordBoundary(t *testing.T) {
	in := "alpha bravo charlie delta echo foxtrot golf hotel india"
	got := preview(in, 25)
	// Should NOT cut mid-word: must end on a real word and the ellipsis.
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected trailing ellipsis: %q", got)
	}
	for _, ch := range []rune{' ', ',', ';', '.', ':', '!', '?'} {
		if strings.HasSuffix(got, string(ch)+"…") {
			t.Errorf("preview should trim trailing punctuation/whitespace before ellipsis, got %q", got)
		}
	}
}

func TestPreviewKeepsValidUTF8(t *testing.T) {
	// Multi-byte runes near the cut point shouldn't produce half-runes.
	in := strings.Repeat("àèìòù", 100)
	got := preview(in, 23)
	for _, r := range got { // would panic / produce U+FFFD on invalid sequences
		_ = r
	}
}

func TestPreviewExactUTF8Boundary(t *testing.T) {
	// "à" is 2 bytes in UTF-8; 23 bytes = 11 full "à" + 1 extra byte → unsafe if not rune-aware
	in := strings.Repeat("à", 12) // 24 bytes total
	got := preview(in, 23)
	// Should not panic or contain replacement char U+FFFD
	for _, r := range got {
		if r == '�' {
			t.Errorf("preview produced invalid UTF-8 rune: %q", got)
		}
	}
	// Must end with ellipsis and be <= 24 bytes (23 + 1 for ellipsis)
	if len(got) > 24 {
		t.Errorf("preview too long: %d bytes (%q)", len(got), got)
	}
}

func TestSummarizeJSONObject(t *testing.T) {
	got := summarizeJSON(`{"alpha":1,"beta":2,"gamma":3}`)
	want := map[string]any{
		"type":      "object",
		"key_count": 3,
		"keys":      []string{"alpha", "beta", "gamma"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSummarizeJSONArray(t *testing.T) {
	got := summarizeJSON(`[1,2,3,4]`)
	want := map[string]any{"type": "array", "len": 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCompactViewForJSONHasSchemaNoValue(t *testing.T) {
	m := &Memory{
		ID: 1, Key: "g", Tags: "graph",
		Value:     `{"a":1,"b":[1,2,3],"c":{"x":1}}`,
		CreatedAt: 100, UpdatedAt: 200,
	}
	v := CompactView(m)
	if v["is_json"] != true {
		t.Errorf("is_json should be true, got %v", v["is_json"])
	}
	if v["compact"] != true {
		t.Errorf("compact should be true")
	}
	if _, ok := v["value"]; ok {
		t.Errorf("compact view must not include 'value'")
	}
	schema, ok := v["schema"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing or wrong type: %T", v["schema"])
	}
	if schema["type"] != "object" || schema["key_count"] != 3 {
		t.Errorf("unexpected schema: %+v", schema)
	}
	hint, _ := v["hint"].(string)
	if !strings.Contains(hint, "memory_query") || !strings.Contains(hint, "expand") {
		t.Errorf("hint should mention memory_query and expand: %q", hint)
	}
}

func TestCompactViewForTextHasPreview(t *testing.T) {
	body := strings.Repeat("alpha bravo charlie ", 50) // ~1 KB of text
	m := &Memory{ID: 2, Key: "t", Value: body}
	v := CompactView(m)
	if v["is_json"] != false {
		t.Errorf("is_json should be false")
	}
	if v["size_bytes"].(int) != len(body) {
		t.Errorf("size_bytes wrong")
	}
	prev, _ := v["preview"].(string)
	if prev == "" || !strings.HasSuffix(prev, "…") {
		t.Errorf("preview should be truncated with ellipsis, got %q", prev)
	}
	if _, ok := v["value"]; ok {
		t.Errorf("compact view must not include 'value'")
	}
}

func TestCompactViewBareScalarFallsBackToTextPreview(t *testing.T) {
	// json.Valid("\"hello\"") returns true, but it isn't a useful schema.
	m := &Memory{Key: "s", Value: `"just a quoted string"`}
	v := CompactView(m)
	if v["is_json"] == true {
		t.Errorf("bare-scalar JSON should not be summarised as a schema")
	}
}
