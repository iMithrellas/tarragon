package plugins

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDiscoverLoadsPluginConfig(t *testing.T) {
	root := t.TempDir()
	plugDir := filepath.Join(root, "example")
	if err := os.MkdirAll(plugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := []byte("" +
		"name=\"example\"\n" +
		"description=\"demo\"\n" +
		"source=\"system\"\n" +
		"enabled=true\n" +
		"entrypoint=\"run.sh\"\n" +
		"lifecycle_mode=\"on_call\"\n")
	if err := os.WriteFile(filepath.Join(plugDir, "plugin.toml"), toml, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	p, ok := m.Plugins["example"]
	if !ok {
		t.Fatalf("plugin not found in manager")
	}
	if p.Config.Name != "example" || p.Config.Entrypoint != "run.sh" || p.Dir != plugDir {
		t.Fatalf("unexpected plugin: %+v", p)
	}
	if p.Config.Source != "system" {
		t.Fatalf("expected source to be loaded, got %q", p.Config.Source)
	}
	if !p.Config.ProvidesGeneral {
		t.Fatalf("expected provides_general_suggestions default true when omitted")
	}
}

func TestDefaultDirContainsHome(t *testing.T) {
	got := DefaultDir()
	if got == "" {
		t.Fatalf("DefaultDir is empty")
	}
	if _, err := os.UserHomeDir(); err == nil {
		if len(got) < 6 || got[0] != '/' {
			t.Fatalf("expected absolute path, got %q", got)
		}
	}
}

func TestStartPersistentSkipsNonDaemonAndDisabled(t *testing.T) {
	m := &Manager{Plugins: make(map[string]*Plugin)}
	m.Plugins["on_call"] = &Plugin{Config: PluginConfig{Name: "on_call", Enabled: true, Entrypoint: "nope.sh", Lifecycle: LifecycleOnCall}}
	m.Plugins["disabled_daemon"] = &Plugin{Config: PluginConfig{Name: "disabled_daemon", Enabled: false, Entrypoint: "nope.sh", Lifecycle: LifecycleDaemon}}
	if err := m.StartPersistent(context.Background(), "ipc:///tmp/test.ipc"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestApplyOverridesMergesSetValues(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	m := &Manager{Plugins: make(map[string]*Plugin)}
	base := PluginConfig{Name: "example", Enabled: true, Prefix: "ex ", Lifecycle: LifecycleOnCall}
	m.Plugins["example"] = &Plugin{Config: base, BaseConfig: base}

	viper.Set("plugins.example.enabled", false)
	viper.Set("plugins.example.prefix", "eg ")
	viper.Set("plugins.example.lifecycle_mode", "daemon")

	if err := m.ApplyOverrides(); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}

	p := m.Plugins["example"]
	if p.Config.Enabled != false {
		t.Fatalf("expected enabled override false, got %v", p.Config.Enabled)
	}
	if p.Config.Prefix != "eg " {
		t.Fatalf("expected prefix override, got %q", p.Config.Prefix)
	}
	if p.Config.Lifecycle != LifecycleDaemon {
		t.Fatalf("expected lifecycle override daemon, got %q", p.Config.Lifecycle)
	}
}

func TestApplyOverridesResetsToBaseWhenOverrideRemoved(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	m := &Manager{Plugins: make(map[string]*Plugin)}
	base := PluginConfig{Name: "example", Enabled: true, Prefix: "ex ", Lifecycle: LifecycleOnCall}
	m.Plugins["example"] = &Plugin{Config: base, BaseConfig: base}

	viper.Set("plugins.example.prefix", "over ")
	if err := m.ApplyOverrides(); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if got := m.Plugins["example"].Config.Prefix; got != "over " {
		t.Fatalf("expected first override to apply, got %q", got)
	}

	viper.Reset()
	if err := m.ApplyOverrides(); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}

	if got := m.Plugins["example"].Config.Prefix; got != "ex " {
		t.Fatalf("expected prefix to revert to base config, got %q", got)
	}
}

func TestResolveEntrypoint(t *testing.T) {
	t.Run("relative", func(t *testing.T) {
		got := ResolveEntrypoint("/tmp/plugin", "run.sh")
		want := filepath.Join("/tmp/plugin", "run.sh")
		if got != want {
			t.Fatalf("ResolveEntrypoint()=%q want %q", got, want)
		}
	})

	t.Run("absolute", func(t *testing.T) {
		const absolute = "/usr/bin/my-plugin"
		got := ResolveEntrypoint("/tmp/plugin", absolute)
		if got != absolute {
			t.Fatalf("ResolveEntrypoint()=%q want %q", got, absolute)
		}
	})
}

func TestDiscoverNewAddsMissingWithoutOverwritingExisting(t *testing.T) {
	root := t.TempDir()

	existingDir := filepath.Join(root, "existing")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDir, "plugin.toml"), []byte(""+
		"name=\"existing\"\n"+
		"enabled=true\n"+
		"entrypoint=\"new.sh\"\n"+
		"lifecycle_mode=\"on_call\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	addedDir := filepath.Join(root, "added")
	if err := os.MkdirAll(addedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(addedDir, "plugin.toml"), []byte(""+
		"name=\"added\"\n"+
		"enabled=true\n"+
		"entrypoint=\"added.sh\"\n"+
		"lifecycle_mode=\"on_call\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	original := &Plugin{Config: PluginConfig{Name: "existing", Entrypoint: "old.sh", Enabled: true, Lifecycle: LifecycleDaemon}}
	original.running.Store(true)

	m := NewManager(root)
	m.Plugins["existing"] = original

	if err := m.DiscoverNew(); err != nil {
		t.Fatalf("DiscoverNew: %v", err)
	}

	if got := m.Plugins["existing"]; got != original {
		t.Fatalf("expected existing plugin pointer preserved")
	}
	if !m.Plugins["existing"].Running() {
		t.Fatalf("expected existing running state to be preserved")
	}

	added, ok := m.Plugins["added"]
	if !ok {
		t.Fatalf("expected newly discovered plugin to be added")
	}
	if added.Config.Entrypoint != "added.sh" {
		t.Fatalf("unexpected added plugin entrypoint: %q", added.Config.Entrypoint)
	}
}

func TestRefreshConfigsUpdatesManifestWithoutReplacingRuntimeState(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "example")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(pluginDir, "plugin.toml")
	if err := os.WriteFile(manifestPath, []byte("name=\"example\"\nenabled=true\nentrypoint=\"old.sh\"\nlifecycle_mode=\"daemon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	original := m.Plugins["example"]

	if err := os.WriteFile(manifestPath, []byte("name=\"example\"\nenabled=true\nentrypoint=\"new.sh\"\nlifecycle_mode=\"daemon\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshConfigs(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if m.Plugins["example"] != original {
		t.Fatal("refresh replaced the plugin instance and lost runtime state")
	}
	if got := original.Config.Entrypoint; got != "new.sh" {
		t.Fatalf("entrypoint = %q, want new.sh", got)
	}
}

func writeManifest(t *testing.T, root, dir, body string) string {
	t.Helper()
	plugDir := filepath.Join(root, dir)
	if err := os.MkdirAll(plugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugDir, "plugin.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return plugDir
}

func TestNormalizePluginID(t *testing.T) {
	cases := map[string]string{
		"system_control":    "system_control",
		"System Control":    "system_control",
		"Mithshell Control": "mithshell_control",
		"  Spaced  Out  ":   "spaced_out",
		"weird!!chars":      "weird_chars",
		"--leading":         "leading",
		"trailing--":        "trailing",
		"":                  "",
		"!!!":               "",
	}
	for input, want := range cases {
		if got := NormalizePluginID(input); got != want {
			t.Errorf("NormalizePluginID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDiscoverKeysByDirectoryWhenIDOmitted(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "system_control", "name=\"System Control\"\nentrypoint=\"run.sh\"\nenabled=true\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	p, ok := m.Plugins["system_control"]
	if !ok {
		t.Fatalf("expected plugin keyed by directory name, got keys %v", pluginKeys(m))
	}
	if p.Config.ID != "system_control" {
		t.Fatalf("expected id system_control, got %q", p.Config.ID)
	}
	if p.Config.Name != "System Control" {
		t.Fatalf("expected display name to be preserved, got %q", p.Config.Name)
	}
}

func TestDiscoverPrefersExplicitIDOverDirectory(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "some_dir", "id=\"chosen\"\nname=\"Whatever\"\nentrypoint=\"run.sh\"\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if _, ok := m.Plugins["chosen"]; !ok {
		t.Fatalf("expected plugin keyed by explicit id, got keys %v", pluginKeys(m))
	}
}

func TestDiscoverNormalizesInvalidExplicitID(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "some_dir", "id=\"Not A Bare Key\"\nentrypoint=\"run.sh\"\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if _, ok := m.Plugins["not_a_bare_key"]; !ok {
		t.Fatalf("expected normalized id, got keys %v", pluginKeys(m))
	}
}

func TestApplyOverridesUsesIDSection(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	root := t.TempDir()
	writeManifest(t, root, "system_control", "name=\"System Control\"\nentrypoint=\"run.sh\"\nprefix=\"@system\"\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	viper.Set("plugins.system_control.prefix", "@sys")
	if err := m.ApplyOverrides(); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if got := m.Plugins["system_control"].Config.Prefix; got != "@sys" {
		t.Fatalf("expected override by id, got %q", got)
	}
}

func TestApplyOverridesFallsBackToDisplayNameSection(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	root := t.TempDir()
	writeManifest(t, root, "system_control", "name=\"System Control\"\nentrypoint=\"run.sh\"\nprefix=\"@system\"\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	viper.Set("plugins.System Control.prefix", "@legacy")
	if err := m.ApplyOverrides(); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if got := m.Plugins["system_control"].Config.Prefix; got != "@legacy" {
		t.Fatalf("expected legacy display-name override to apply, got %q", got)
	}
}

func TestApplyOverridesPrefersIDOverDisplayName(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	root := t.TempDir()
	writeManifest(t, root, "system_control", "name=\"System Control\"\nentrypoint=\"run.sh\"\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	viper.Set("plugins.System Control.prefix", "@legacy")
	viper.Set("plugins.system_control.prefix", "@sys")
	if err := m.ApplyOverrides(); err != nil {
		t.Fatalf("apply overrides: %v", err)
	}
	if got := m.Plugins["system_control"].Config.Prefix; got != "@sys" {
		t.Fatalf("expected id section to win, got %q", got)
	}
}

func TestValidateOverrideKeysReportsProblems(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	root := t.TempDir()
	writeManifest(t, root, "system_control", "name=\"System Control\"\nentrypoint=\"run.sh\"\n")

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}

	viper.Set("plugins.system_control.enabled", true)
	viper.Set("plugins.system_control.prefx", "typo")
	viper.Set("plugins.System Control.enabled", true)
	viper.Set("plugins.ghost.enabled", true)

	warnings := m.ValidateOverrideKeys()
	joined := strings.Join(warnings, "\n")

	for _, want := range []string{"prefx", "ghost", "display name"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected warning mentioning %q, got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "\"enabled\"") {
		t.Fatalf("did not expect a warning for a supported key, got:\n%s", joined)
	}
}

func pluginKeys(m *Manager) []string {
	keys := make([]string, 0, len(m.Plugins))
	for k := range m.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
