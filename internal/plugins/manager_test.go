package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
