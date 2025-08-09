package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/iMithrellas/tarragon/internal/plugins"
)

// Remove stale ipc files to avoid EADDRINUSE on restart.
func cleanupIPC(endpoint string) {
	if len(endpoint) >= 6 && endpoint[:6] == "ipc://" {
		path := endpoint[6:]
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if _, err := os.Stat(path); err == nil {
			_ = os.Remove(path)
		}
	}
}

// invokeOnCall runs a plugin entrypoint once with the given query and returns its JSON output.
func invokeOnCall(ctx context.Context, p *plugins.Plugin, query string) (json.RawMessage, error) {
	entry := filepath.Join(p.Dir, p.Config.Entrypoint)
	cmd := exec.CommandContext(ctx, entry, "--once", query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w; stderr=%s", p.Config.Name, err, stderr.String())
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		out = []byte("{}")
	}
	return json.RawMessage(out), nil
}

// escapeJSONString escapes a string for embedding into JSON literals.
func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
