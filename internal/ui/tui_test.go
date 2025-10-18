package ui

import (
	"encoding/json"
	"testing"
)

func TestExtractChoices_VariantsWrapped(t *testing.T) {
	// aggResult-style wrapper with data.variants as array of objects
	raw := json.RawMessage(`{"elapsed_ms":12.3,"data":{"variants":[{"id":"1","label":"One"},{"id":"2","title":"Two"},{"id":"3","text":"Three"}]}}`)
	ch := extractChoices(raw)
	if len(ch) != 3 {
		t.Fatalf("expected 3 choices, got %d", len(ch))
	}
	if ch[0].ID != "1" || ch[0].Label != "One" {
		t.Fatalf("unexpected first choice: %+v", ch[0])
	}
	if ch[1].ID != "2" || ch[1].Label != "Two" {
		t.Fatalf("unexpected second choice: %+v", ch[1])
	}
	if ch[2].ID != "3" || ch[2].Label != "Three" {
		t.Fatalf("unexpected third choice: %+v", ch[2])
	}
}

func TestExtractChoices_ArrayStrings(t *testing.T) {
	// wrapper with data array of strings
	raw := json.RawMessage(`{"elapsed_ms":1,"data":["alpha","beta"]}`)
	ch := extractChoices(raw)
	if len(ch) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(ch))
	}
	if ch[0].ID != "alpha" || ch[0].Label != "alpha" {
		t.Fatalf("unexpected first choice: %+v", ch[0])
	}
	if ch[1].ID != "beta" || ch[1].Label != "beta" {
		t.Fatalf("unexpected second choice: %+v", ch[1])
	}
}

func TestParseArrayChoices_ObjectFallbacks(t *testing.T) {
	// top-level array of objects with different label keys
	raw := json.RawMessage(`[{"id":"a","label":"A"},{"id":"b","title":"B"},{"id":"c","text":"C"},{"label":"D-no-id"}]`)
	ch := parseArrayChoices(raw)
	if len(ch) != 4 {
		t.Fatalf("expected 4 choices, got %d", len(ch))
	}
	if ch[0].ID != "a" || ch[0].Label != "A" {
		t.Fatalf("unexpected ch[0]: %+v", ch[0])
	}
	if ch[1].ID != "b" || ch[1].Label != "B" {
		t.Fatalf("unexpected ch[1]: %+v", ch[1])
	}
	if ch[2].ID != "c" || ch[2].Label != "C" {
		t.Fatalf("unexpected ch[2]: %+v", ch[2])
	}
	if ch[3].ID != "D-no-id" || ch[3].Label != "D-no-id" {
		t.Fatalf("unexpected ch[3]: %+v", ch[3])
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in  string
		w   int
		out string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hi", 1, "h"},
		{"", 3, ""},
	}
	for _, c := range cases {
		got := truncate(c.in, c.w)
		if got != c.out {
			t.Fatalf("truncate(%q,%d) = %q; want %q", c.in, c.w, got, c.out)
		}
	}
}
