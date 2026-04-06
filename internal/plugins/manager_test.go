package plugins

import (
	"context"
	"os"
	"path/filepath"
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
