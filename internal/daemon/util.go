package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mithrel-dots/tarragon/internal/plugins"
	"github.com/mithrel-dots/tarragon/internal/wire"
)

// onCallEnv gives ephemeral plugins the same identity and prefix context that
// persistent plugins receive when the daemon starts them.
func onCallEnv(p *plugins.Plugin) []string {
	return append(os.Environ(),
		fmt.Sprintf("TARRAGON_PLUGIN_NAME=%s", p.Config.ID),
		fmt.Sprintf("TARRAGON_PLUGIN_ID=%s", p.Config.ID),
		fmt.Sprintf("TARRAGON_PLUGIN_DISPLAY_NAME=%s", p.Config.Name),
		fmt.Sprintf("TARRAGON_PREFIX_SYMBOL=%s", plugins.PrefixSymbol()),
	)
}

// invokeOnCallQuery runs an on-call plugin query command and returns its JSON output.
func invokeOnCallQuery(ctx context.Context, p *plugins.Plugin, query string) (json.RawMessage, error) {
	entry, err := resolveOnCallEntrypoint(p)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, entry, "tarragon", "query", query)
	cmd.Env = onCallEnv(p)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w; stderr=%s", p.Config.ID, err, stderr.String())
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		out = []byte("{}")
	}
	return json.RawMessage(out), nil
}

// invokeOnCallSelect runs an on-call plugin select command.
//
// The command contract is:
//
//	<entrypoint> tarragon select <result-id> [action]
//
// If stdout is empty and exit status is zero, the action is treated as success.
// If stdout contains JSON, it may include {"success": bool, "message": string}.
func invokeOnCallSelect(ctx context.Context, p *plugins.Plugin, resultID, action string) (wire.SelectResponse, error) {
	entry, err := resolveOnCallEntrypoint(p)
	if err != nil {
		return wire.SelectResponse{}, err
	}
	args := []string{"tarragon", "select", resultID}
	if strings.TrimSpace(action) != "" {
		args = append(args, action)
	}

	cmd := exec.CommandContext(ctx, entry, args...)
	cmd.Env = onCallEnv(p)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wire.SelectResponse{}, fmt.Errorf("%s: %w; stderr=%s", p.Config.ID, err, stderr.String())
	}

	resp := wire.SelectResponse{Type: wire.MsgSelectResponse, Success: true}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return resp, nil
	}

	var parsed struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return wire.SelectResponse{}, fmt.Errorf("%s: invalid select response JSON: %w", p.Config.ID, err)
	}
	if parsed.Success != nil {
		resp.Success = *parsed.Success
	}
	resp.Message = parsed.Message
	return resp, nil
}

// resolveOnCallEntrypoint recovers system plugins whose executable moved after
// installation. Local plugin entrypoints remain pinned to their manifest path.
func resolveOnCallEntrypoint(p *plugins.Plugin) (string, error) {
	entry := plugins.ResolveEntrypoint(p.Dir, p.Config.Entrypoint)
	if info, err := os.Stat(entry); err == nil && !info.IsDir() {
		return entry, nil
	}
	if p.Config.Source == "system" {
		name := filepath.Base(p.Config.Entrypoint)
		if resolved, err := exec.LookPath(name); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%s: entrypoint %q is unavailable", p.Config.ID, entry)
}

// escapeJSONString escapes a string for embedding into JSON literals.
func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
