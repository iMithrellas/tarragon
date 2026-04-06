package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iMithrellas/tarragon/internal/plugins"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

func TestInvokeOnCall_Success(t *testing.T) {
	d := t.TempDir()
	// Simple echo JSON plugin
	writeScript(t, d, "plug.sh", `#!/usr/bin/env bash
if [[ "$1" == "--once" ]]; then echo "{\"ok\":true,\"echo\":\"$2\"}"; exit 0; fi
`)
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: "plug.sh", Enabled: true}}
	out, err := invokeOnCall(context.Background(), p, "abc")
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if string(out) != `{"ok":true,"echo":"abc"}` {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestInvokeOnCall_Timeout(t *testing.T) {
	d := t.TempDir()
	// Script that sleeps; context timeout should kill it
	writeScript(t, d, "slow.sh", "#!/usr/bin/env bash\nsleep 1\n")
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: "slow.sh", Enabled: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000) // 10ms
	defer cancel()
	if _, err := invokeOnCall(ctx, p, "x"); err == nil {
		t.Fatalf("expected timeout error")
	}
}

func TestInvokeOnCall_AbsoluteEntrypoint(t *testing.T) {
	d := t.TempDir()
	entry := writeScript(t, d, "plug-abs.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"--once\" ]]; then echo '{\"ok\":true,\"data\":\"abs\"}'; fi\n")
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: entry, Enabled: true}}
	out, err := invokeOnCall(context.Background(), p, "ignored")
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if string(out) != `{"ok":true,"data":"abs"}` {
		t.Fatalf("unexpected output: %s", string(out))
	}
}
