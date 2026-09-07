package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSubstituteArgs(t *testing.T) {
	tests := []struct {
		name     string
		template []string
		want     []string
	}{
		{"placeholder", []string{"bg", "--set", "{path}"}, []string{"bg", "--set", "/w.png"}},
		{"appended", []string{"bg", "--set"}, []string{"bg", "--set", "/w.png"}},
		{"embedded", []string{"bg", "file://{path}"}, []string{"bg", "file:///w.png"}},
		{"multiple", []string{"bg", "{path}", "{path}"}, []string{"bg", "/w.png", "/w.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substituteArgs(tt.template, "/w.png")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanOutput(t *testing.T) {
	in := "Error: \n   0: \x1b[91mFailed to get source color.\x1b[0m\n\n   1: nope\n"
	want := "Error:; 0: Failed to get source color.; 1: nope"
	if got := cleanOutput(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMatchToken(t *testing.T) {
	const hay = "nature/forest_dawn.png forest dawn"

	if _, ok := matchToken(hay, "zzz"); ok {
		t.Fatal("expected no match for 'zzz'")
	}

	sub, ok := matchToken(hay, "forest")
	if !ok {
		t.Fatal("expected substring match for 'forest'")
	}
	seq, ok := matchToken(hay, "ntrfd")
	if !ok {
		t.Fatal("expected subsequence match for 'ntrfd'")
	}
	if sub <= seq {
		t.Fatalf("substring score %v should beat subsequence score %v", sub, seq)
	}
}

func TestPrettify(t *testing.T) {
	for in, want := range map[string]string{
		"cyber_city-02":   "cyber city 02",
		"mountain--lake":  "mountain lake",
		"already pretty":  "already pretty",
		"dots.in.name":    "dots in name",
		"__leading_trail": "leading trail",
	} {
		if got := prettify(in); got != want {
			t.Errorf("prettify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLibraryScanAndSearch(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "nature", "forest_dawn.png"))
	mustWrite(t, filepath.Join(root, "nature", "mountain-lake.jpg"))
	mustWrite(t, filepath.Join(root, "abstract", "cyber_city_02.png"))
	mustWrite(t, filepath.Join(root, "readme.txt"))
	mustWrite(t, filepath.Join(root, ".hidden", "secret.png"))

	cfg := defaultConfig()
	cfg.normalize()

	lib := NewLibrary([]string{root})
	if err := lib.Scan(context.Background(), cfg); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// readme.txt filtered by extension, .hidden skipped.
	if got := lib.Len(); got != 3 {
		t.Fatalf("indexed %d wallpapers, want 3: %+v", got, lib.All())
	}

	hits := lib.Search("forest", 10)
	if len(hits) != 1 || hits[0].Entry.Name != "forest_dawn" {
		t.Fatalf("search 'forest' = %+v", hits)
	}

	// Multi-token queries must AND across path and name.
	if hits := lib.Search("nature lake", 10); len(hits) != 1 || hits[0].Entry.Name != "mountain-lake" {
		t.Fatalf("search 'nature lake' = %+v", hits)
	}
	if hits := lib.Search("nature cyber", 10); len(hits) != 0 {
		t.Fatalf("search 'nature cyber' should be empty, got %+v", hits)
	}

	// Empty query lists everything, bounded by the limit.
	if hits := lib.Search("", 2); len(hits) != 2 {
		t.Fatalf("empty query with limit 2 returned %d", len(hits))
	}

	if _, ok := lib.Lookup(filepath.Join(root, "nature", "forest_dawn.png")); !ok {
		t.Fatal("lookup by absolute path failed")
	}

	// Random must avoid the excluded path when alternatives exist.
	exclude := filepath.Join(root, "nature", "forest_dawn.png")
	for i := 0; i < 20; i++ {
		e, ok := lib.Random(exclude)
		if !ok {
			t.Fatal("random returned nothing")
		}
		if e.Path == exclude {
			t.Fatal("random returned the excluded entry")
		}
	}
}

func TestConfigDefaultsAndNormalize(t *testing.T) {
	t.Setenv("HOME", "/home/tester")

	cfg := defaultConfig()
	cfg.Directories = []string{"~/Walls", "$HOME/Walls", "  ", "/tmp/walls/"}
	cfg.Extensions = []string{".PNG", "jpg", ""}
	cfg.Backend = "  AWWW "
	cfg.MaxResults = 0
	cfg.normalize()

	// ~ and $HOME expand to the same path and deduplicate.
	want := []string{"/home/tester/Walls", "/tmp/walls"}
	if !reflect.DeepEqual(cfg.Directories, want) {
		t.Fatalf("directories = %q, want %q", cfg.Directories, want)
	}
	if !reflect.DeepEqual(cfg.Extensions, []string{"png", "jpg"}) {
		t.Fatalf("extensions = %q", cfg.Extensions)
	}
	if cfg.Backend != "awww" {
		t.Fatalf("backend = %q", cfg.Backend)
	}
	if cfg.MaxResults != 40 {
		t.Fatalf("max_results = %d, want fallback 40", cfg.MaxResults)
	}

	if !cfg.allowedExt("a.PNG") || !cfg.allowedExt("a.jpg") {
		t.Fatal("allowedExt should be case-insensitive")
	}
	if cfg.allowedExt("a.txt") || cfg.allowedExt("noext") {
		t.Fatal("allowedExt should reject unlisted/extensionless files")
	}

	// Empty directory list falls back to the built-in candidates.
	cfg2 := defaultConfig()
	cfg2.normalize()
	if len(cfg2.Directories) == 0 {
		t.Fatal("expected default directories")
	}
}

func TestMatugenArgsOLED(t *testing.T) {
	cfg := defaultConfig()
	cfg.MatugenOLED = true
	args := matugenArgs("/wallpapers/night.png", cfg)
	want := []string{"image", "/wallpapers/night.png", "--mode", "dark", "--type", "scheme-tonal-spot", "--prefer", "saturation", "--lightness-dark", "-0.2"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("matugen args = %q, want %q", args, want)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// First load writes the template.
	if _, path, err := loadConfig(); err != nil {
		t.Fatalf("first load: %v", err)
	} else if _, err := os.Stat(path); err != nil {
		t.Fatalf("template not written: %v", err)
	}

	// Second load parses it back without error and keeps defaults.
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !cfg.RestoreOnStart || cfg.Backend != "matugen" {
		t.Fatalf("unexpected config from template: %+v", cfg)
	}
	if cfg.MatugenPrefer == "" {
		t.Fatal("matugen_prefer must default to a non-empty value")
	}

	// Partial config keeps defaults for absent keys.
	path := filepath.Join(dir, "tarragon", "wallpaper.toml")
	if err := os.WriteFile(path, []byte("backend = \"swaybg\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = loadConfig()
	if err != nil {
		t.Fatalf("partial load: %v", err)
	}
	if cfg.Backend != "swaybg" {
		t.Fatalf("backend = %q", cfg.Backend)
	}
	if cfg.MaxResults != 40 {
		t.Fatalf("defaults lost on partial config: %+v", cfg)
	}
}

func TestStatePersistenceAndHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	st := loadState()
	if st.Current() != "" {
		t.Fatal("fresh state should be empty")
	}

	st.Record("/a.png", "awww")
	st.Record("/b.png", "awww")
	st.Record("/a.png", "awww") // re-selecting must not duplicate history
	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded := loadState()
	if reloaded.Current() != "/a.png" {
		t.Fatalf("current = %q", reloaded.Current())
	}
	if !reflect.DeepEqual(reloaded.History, []string{"/a.png", "/b.png"}) {
		t.Fatalf("history = %q", reloaded.History)
	}
	if reloaded.Backend != "awww" {
		t.Fatalf("backend = %q", reloaded.Backend)
	}
}

func TestStateSurvivesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	p := statePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Must not panic and must not block startup.
	if st := loadState(); st.Current() != "" {
		t.Fatalf("expected empty state, got %q", st.Current())
	}
}

func TestResolveBackendErrors(t *testing.T) {
	cfg := defaultConfig()
	cfg.Backend = "custom"
	cfg.normalize()
	if _, err := resolveBackend(cfg); err == nil {
		t.Fatal("custom backend without custom_command should error")
	}

	cfg.Backend = "definitely-not-a-backend"
	if _, err := resolveBackend(cfg); err == nil {
		t.Fatal("unknown backend should error")
	}
}

func TestSelectionResolution(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.png"))
	mustWrite(t, filepath.Join(root, "b.png"))

	cfg := defaultConfig()
	cfg.Directories = []string{root}
	cfg.normalize()

	p := &plugin{
		cfg:   cfg,
		lib:   NewLibrary(cfg.Directories),
		state: &State{path: filepath.Join(t.TempDir(), "state.json")},
		log:   &Logger{name: "test"},
	}
	if err := p.lib.Scan(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := p.resolveSelection(""); err == nil {
		t.Fatal("empty id should error")
	}
	if _, err := p.resolveSelection(previousID); err == nil {
		t.Fatal("previous with no history should error")
	}
	if got, err := p.resolveSelection(randomID); err != nil || got == "" {
		t.Fatalf("random = %q, %v", got, err)
	}
	if got, err := p.resolveSelection("/x/y.png"); err != nil || got != "/x/y.png" {
		t.Fatalf("literal id = %q, %v", got, err)
	}

	p.state.Record("/a.png", "custom")
	p.state.Record("/b.png", "custom")
	if got, err := p.resolveSelection(previousID); err != nil || got != "/a.png" {
		t.Fatalf("previous = %q, %v", got, err)
	}
}

func TestBuildResultsShape(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "scenery", "forest_dawn.png"))

	cfg := defaultConfig()
	cfg.Directories = []string{root}
	cfg.normalize()

	p := &plugin{
		cfg:   cfg,
		lib:   NewLibrary(cfg.Directories),
		state: &State{path: filepath.Join(t.TempDir(), "state.json")},
		log:   &Logger{name: "test"},
	}
	if err := p.lib.Scan(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data := p.buildResults("forest")
	if len(data.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(data.Results))
	}
	got := data.Results[0]
	if got.ID != filepath.Join(root, "scenery", "forest_dawn.png") {
		t.Fatalf("id = %q", got.ID)
	}
	if got.PreviewPath != got.ID {
		t.Fatalf("preview_path %q should equal id %q", got.PreviewPath, got.ID)
	}
	if got.Label != "forest dawn" {
		t.Fatalf("label = %q", got.Label)
	}
	if len(got.Actions) == 0 || !got.Actions[0].Default {
		t.Fatalf("first action must be the default: %+v", got.Actions)
	}

	// Empty query surfaces the synthetic entries.
	all := p.buildResults("")
	if all.Results[0].ID != randomID {
		t.Fatalf("expected random entry first, got %q", all.Results[0].ID)
	}

	// An empty library reports an actionable error instead of silence.
	empty := &plugin{
		cfg:   cfg,
		lib:   NewLibrary([]string{filepath.Join(root, "nope")}),
		state: p.state,
		log:   p.log,
	}
	if err := empty.lib.Scan(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if data := empty.buildResults("x"); data.Error == "" {
		t.Fatal("empty library should report an error payload")
	}
}

func TestHumanSize(t *testing.T) {
	for in, want := range map[int64]string{
		512:             "512 B",
		2048:            "2.0 KiB",
		5 * 1024 * 1024: "5.0 MiB",
		3 * 1 << 30:     "3.0 GiB",
	} {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
