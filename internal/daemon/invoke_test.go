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

func TestInvokeOnCallQuery_Success(t *testing.T) {
	d := t.TempDir()
	// Simple echo JSON plugin
	writeScript(t, d, "plug.sh", `#!/usr/bin/env bash
if [[ "$1" == "tarragon" && "$2" == "query" ]]; then echo "{\"ok\":true,\"echo\":\"$3\"}"; exit 0; fi
`)
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: "plug.sh", Enabled: true}}
	out, err := invokeOnCallQuery(context.Background(), p, "abc")
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if string(out) != `{"ok":true,"echo":"abc"}` {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestInvokeOnCallQuery_Timeout(t *testing.T) {
	d := t.TempDir()
	// Script that sleeps; context timeout should kill it
	writeScript(t, d, "slow.sh", "#!/usr/bin/env bash\nsleep 1\n")
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: "slow.sh", Enabled: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000) // 10ms
	defer cancel()
	if _, err := invokeOnCallQuery(ctx, p, "x"); err == nil {
		t.Fatalf("expected timeout error")
	}
}

func TestInvokeOnCallQuery_AbsoluteEntrypoint(t *testing.T) {
	d := t.TempDir()
	entry := writeScript(t, d, "plug-abs.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"query\" ]]; then echo '{\"ok\":true,\"data\":\"abs\"}'; fi\n")
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: entry, Enabled: true}}
	out, err := invokeOnCallQuery(context.Background(), p, "ignored")
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if string(out) != `{"ok":true,"data":"abs"}` {
		t.Fatalf("unexpected output: %s", string(out))
	}
}

func TestInvokeOnCallSelect_SuccessWithJSON(t *testing.T) {
	d := t.TempDir()
	writeScript(t, d, "select.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"select\" ]]; then echo '{\"success\":true,\"message\":\"done\"}'; exit 0; fi\nexit 1\n")
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: "select.sh", Enabled: true}}
	resp, err := invokeOnCallSelect(context.Background(), p, "id-1", "open")
	if err != nil {
		t.Fatalf("invoke select error: %v", err)
	}
	if !resp.Success || resp.Message != "done" {
		t.Fatalf("unexpected select response: %+v", resp)
	}
}

func TestInvokeOnCallSelect_DefaultSuccessWhenNoOutput(t *testing.T) {
	d := t.TempDir()
	writeScript(t, d, "select-empty.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"select\" ]]; then exit 0; fi\nexit 1\n")
	p := &plugins.Plugin{Dir: d, Config: plugins.PluginConfig{Name: "t", Entrypoint: "select-empty.sh", Enabled: true}}
	resp, err := invokeOnCallSelect(context.Background(), p, "id-2", "")
	if err != nil {
		t.Fatalf("invoke select error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success response, got %+v", resp)
	}
}
