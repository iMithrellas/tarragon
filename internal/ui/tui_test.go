package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/iMithrellas/tarragon/internal/wire"
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

// TestPluginColor verifies that pluginColor returns a consistent, palette-bound
// color for any plugin name and that different names can yield different colors.
func TestPluginColor(t *testing.T) {
	// Determinism: same name must always return the same color
	c1 := pluginColor("calc_python")
	c2 := pluginColor("calc_python")
	if c1 != c2 {
		t.Fatalf("pluginColor is not deterministic: %q vs %q for same name", c1, c2)
	}

	// Coverage: ensure at least two distinct colors appear across varied names
	names := []string{"calc_python", "template_python", "template_rust", "search_engine", "fzf", "web", "math", "notes"}
	seen := make(map[string]bool)
	for _, n := range names {
		seen[string(pluginColor(n))] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected ≥2 distinct colors from %d names, got %d", len(names), len(seen))
	}

	// Validity: result must be one of the defined palette entries
	col := pluginColor("any_plugin")
	found := false
	for _, p := range pluginColors {
		if col == p {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pluginColor returned %q which is not in the palette", col)
	}
}

// TestUpdateDetailContent verifies that after updating state the viewport
// content is populated with properly pretty-printed JSON including elapsed_ms.
func TestUpdateDetailContent(t *testing.T) {
	m := NewModel("test-client", 200)
	m.detail.Width = 60
	m.detail.Height = 20
	m.results = map[string]tuiAggResult{
		"calc_python": {
			ElapsedMs: 2.5,
			Data:      json.RawMessage(`{"answer":42}`),
		},
	}
	m.rows = []wire.ResultItem{{ID: "1", Label: "One", Plugin: "calc_python"}}
	m.cursor = 0
	m.updateDetailContent()

	content := m.detail.View()
	if !strings.Contains(content, "elapsed_ms") {
		t.Fatalf("detail content missing 'elapsed_ms', got: %q", content)
	}
	if !strings.Contains(content, "2.5") {
		t.Fatalf("detail content missing '2.5', got: %q", content)
	}
	if !strings.Contains(content, "answer") {
		t.Fatalf("detail content missing 'answer', got: %q", content)
	}
}

// TestUpdateDetailContent_NoSelection verifies fallback text when no row is selected.
func TestUpdateDetailContent_NoSelection(t *testing.T) {
	m := NewModel("test-client", 200)
	m.detail.Width = 60
	m.detail.Height = 20
	// no rows
	m.updateDetailContent()

	content := m.detail.View()
	if !strings.Contains(content, "no selection") {
		t.Fatalf("expected '(no selection)', got: %q", content)
	}
}

// TestPluginColorEmpty verifies empty string doesn't panic and returns a palette color.
func TestPluginColorEmpty(t *testing.T) {
	col := pluginColor("")
	found := false
	for _, p := range pluginColors {
		if col == p {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pluginColor(\"\") returned %q outside palette", col)
	}
}

func TestHasPendingPluginStates(t *testing.T) {
	if !hasPendingPluginStates(map[string]tuiPluginState{"websearch": {State: "pending"}}) {
		t.Fatalf("expected pending state to be detected")
	}
	if hasPendingPluginStates(map[string]tuiPluginState{"calc": {State: "done"}, "files": {State: "empty"}}) {
		t.Fatalf("did not expect pending state")
	}
}

func TestRenderQueryPluginStatus(t *testing.T) {
	now := time.UnixMilli(2500)
	m := NewModel("test-client", 200)
	m.queryStartedAtMs = 1000
	m.queryPlugins = map[string]tuiPluginState{
		"calc":      {State: "done", Count: 2},
		"files":     {State: "empty"},
		"websearch": {State: "pending"},
		"notes":     {State: "error", Error: "timeout"},
	}

	got := m.renderQueryPluginStatus(now)
	for _, want := range []string{"Query 3/4 done", "1 empty", "1 error", "websearch pending 1.5s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestRenderFooterPluginStates(t *testing.T) {
	now := time.UnixMilli(2500)
	m := NewModel("test-client", 200)
	m.width = 200
	m.queryStartedAtMs = 1000
	m.queryPlugins = map[string]tuiPluginState{
		"calc":      {State: "done", Count: 2},
		"files":     {State: "empty"},
		"websearch": {State: "pending"},
		"notes":     {State: "error", Error: "timeout"},
	}

	got := m.renderFooterPluginStates(now)
	for _, want := range []string{"calc: done (2)", "files: empty", "notes: error: timeout", "websearch: pending 1.5s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}
