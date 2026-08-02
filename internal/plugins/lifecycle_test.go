package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePluginScript installs a shell-script plugin and returns the manager
// holding it. The script writes a line to logPath on startup, and another on
// SIGTERM when trapSigterm is set, which lets tests distinguish a graceful
// shutdown from a kill.
func writePluginScript(t *testing.T, name, logPath string, trapSigterm bool) *Manager {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	trap := ""
	if trapSigterm {
		trap = "trap 'echo terminated >> \"" + logPath + "\"; exit 0' TERM\n"
	} else {
		trap = "trap '' TERM\n" // deliberately ignore SIGTERM
	}
	script := "#!/bin/sh\n" +
		trap +
		"echo started >> \"" + logPath + "\"\n" +
		"while true; do sleep 0.05; done\n"

	entry := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(entry, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := "name=\"" + name + "\"\n" +
		"enabled=true\n" +
		"entrypoint=\"run.sh\"\n" +
		"lifecycle_mode=\"daemon\"\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	return m
}

func waitForFileContains(t *testing.T, path, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(b), want) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestStopSendsSigtermBeforeKilling(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "plugin.log")
	m := writePluginScript(t, "graceful", logPath, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.StartPersistent(ctx, "/tmp/does-not-matter.sock"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitForFileContains(t, logPath, "started", 3*time.Second) {
		t.Fatal("plugin never started")
	}
	if !m.IsRunning("graceful") {
		t.Fatal("plugin should report running")
	}

	start := time.Now()
	m.StopAll()
	elapsed := time.Since(start)

	if !waitForFileContains(t, logPath, "terminated", 2*time.Second) {
		t.Fatal("plugin was not given the chance to handle SIGTERM")
	}
	// A graceful exit must return well inside the grace period rather than
	// blocking for the whole of it.
	if elapsed > 3*time.Second {
		t.Fatalf("StopAll took %s, expected a prompt graceful exit", elapsed)
	}
	if m.IsRunning("graceful") {
		t.Fatal("plugin should no longer be running")
	}
}

func TestStopEscalatesToKillWhenSigtermIgnored(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "plugin.log")
	m := writePluginScript(t, "stubborn", logPath, false)
	m.SetStopTimeout(300 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.StartPersistent(ctx, "/tmp/does-not-matter.sock"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitForFileContains(t, logPath, "started", 3*time.Second) {
		t.Fatal("plugin never started")
	}

	start := time.Now()
	m.StopAll()
	elapsed := time.Since(start)

	if m.IsRunning("stubborn") {
		t.Fatal("plugin should have been killed")
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("StopAll returned in %s, expected it to honour the grace period first", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("StopAll took %s, escalation to SIGKILL was too slow", elapsed)
	}
}

func TestStopTimeoutConfiguration(t *testing.T) {
	m := NewManager(t.TempDir())
	if got := m.StopTimeout(); got != DefaultStopTimeout {
		t.Fatalf("default stop timeout = %s, want %s", got, DefaultStopTimeout)
	}
	m.SetStopTimeout(2 * time.Second)
	if got := m.StopTimeout(); got != 2*time.Second {
		t.Fatalf("stop timeout = %s, want 2s", got)
	}
	// Non-positive values must fall back rather than disable the grace period.
	m.SetStopTimeout(0)
	if got := m.StopTimeout(); got != DefaultStopTimeout {
		t.Fatalf("stop timeout = %s, want %s", got, DefaultStopTimeout)
	}
}

func TestStopAllIsIdempotentAndSafeWhenNotRunning(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "plugin.log")
	m := writePluginScript(t, "graceful", logPath, true)

	// Never started: must not panic or block.
	m.StopAll()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.StartPersistent(ctx, "/tmp/does-not-matter.sock"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitForFileContains(t, logPath, "started", 3*time.Second) {
		t.Fatal("plugin never started")
	}
	m.StopAll()
	m.StopAll()
}

func TestRestartBouncesDaemonPlugin(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "plugin.log")
	m := writePluginScript(t, "graceful", logPath, true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.StartPersistent(ctx, "/tmp/does-not-matter.sock"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitForFileContains(t, logPath, "started", 3*time.Second) {
		t.Fatal("plugin never started")
	}

	firstPID := m.Plugins["graceful"].cmd.Process.Pid

	results := m.Restart(ctx, "/tmp/does-not-matter.sock", nil)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if results[0].Name != "graceful" || results[0].Status != RestartStatusRestarted {
		t.Fatalf("unexpected result: %+v", results[0])
	}
	if !waitForFileContains(t, logPath, "terminated", 2*time.Second) {
		t.Fatal("restart did not stop the old process gracefully")
	}
	if !m.IsRunning("graceful") {
		t.Fatal("plugin should be running again after restart")
	}
	if newPID := m.Plugins["graceful"].cmd.Process.Pid; newPID == firstPID {
		t.Fatalf("expected a new process, still pid %d", newPID)
	}

	m.StopAll()
}

func TestRestartReportsPerLifecycleBehaviour(t *testing.T) {
	root := t.TempDir()
	write := func(name, lifecycle string, enabled bool) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := "name=\"" + name + "\"\n" +
			"enabled=" + boolString(enabled) + "\n" +
			"entrypoint=\"run.sh\"\n" +
			"lifecycle_mode=\"" + lifecycle + "\"\n"
		if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("oncall", "on_call", true)
	write("ondemand", "on_demand_persistent", true)
	write("disabled", "daemon", false)

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}

	results := m.Restart(context.Background(), "/tmp/does-not-matter.sock", nil)
	byName := make(map[string]RestartResult, len(results))
	for _, r := range results {
		byName[r.Name] = r
	}

	if got := byName["oncall"].Status; got != RestartStatusSkipped {
		t.Fatalf("on_call status = %q, want %q", got, RestartStatusSkipped)
	}
	if got := byName["ondemand"].Status; got != RestartStatusStopped {
		t.Fatalf("on_demand_persistent status = %q, want %q", got, RestartStatusStopped)
	}
	if got := byName["disabled"].Status; got != RestartStatusStopped {
		t.Fatalf("disabled status = %q, want %q", got, RestartStatusStopped)
	}

	// Results are sorted by name so CLI output is stable.
	for i := 1; i < len(results); i++ {
		if results[i-1].Name > results[i].Name {
			t.Fatalf("results are not sorted: %+v", results)
		}
	}
}

func TestRestartUnknownPluginReportsError(t *testing.T) {
	m := NewManager(t.TempDir())
	results := m.Restart(context.Background(), "/tmp/does-not-matter.sock", []string{"ghost"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %+v", results)
	}
	if results[0].Status != RestartStatusError {
		t.Fatalf("status = %q, want %q", results[0].Status, RestartStatusError)
	}
}

func TestRestartFailureIsReported(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Manifest points at an entrypoint that does not exist.
	manifest := "name=\"broken\"\nenabled=true\nentrypoint=\"missing.sh\"\nlifecycle_mode=\"daemon\"\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(root)
	if err := m.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}

	results := m.Restart(context.Background(), "/tmp/does-not-matter.sock", []string{"broken"})
	if len(results) != 1 || results[0].Status != RestartStatusError {
		t.Fatalf("expected an error result, got %+v", results)
	}
	if results[0].Message == "" {
		t.Fatal("error result should carry a message")
	}
}

func TestContextCancellationStopsPluginsGracefully(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "plugin.log")
	m := writePluginScript(t, "graceful", logPath, true)

	ctx, cancel := context.WithCancel(context.Background())
	if err := m.StartPersistent(ctx, "/tmp/does-not-matter.sock"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitForFileContains(t, logPath, "started", 3*time.Second) {
		t.Fatal("plugin never started")
	}

	// Cancelling the context must SIGTERM the plugin, not SIGKILL it, which is
	// what exec.CommandContext would do by default.
	cancel()

	if !waitForFileContains(t, logPath, "terminated", 3*time.Second) {
		t.Fatal("context cancellation did not deliver SIGTERM to the plugin")
	}
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
