package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

// invokeOnCallQuery runs an on-call plugin query command and returns its JSON output.
func invokeOnCallQuery(ctx context.Context, p *plugins.Plugin, query string) (json.RawMessage, error) {
	entry := plugins.ResolveEntrypoint(p.Dir, p.Config.Entrypoint)
	cmd := exec.CommandContext(ctx, entry, "tarragon", "query", query)
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

// invokeOnCallSelect runs an on-call plugin select command.
//
// The command contract is:
//
//	<entrypoint> tarragon select <result-id> [action]
//
// If stdout is empty and exit status is zero, the action is treated as success.
// If stdout contains JSON, it may include {"success": bool, "message": string}.
func invokeOnCallSelect(ctx context.Context, p *plugins.Plugin, resultID, action string) (wire.SelectResponse, error) {
	entry := plugins.ResolveEntrypoint(p.Dir, p.Config.Entrypoint)
	args := []string{"tarragon", "select", resultID}
	if strings.TrimSpace(action) != "" {
		args = append(args, action)
	}

	cmd := exec.CommandContext(ctx, entry, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wire.SelectResponse{}, fmt.Errorf("%s: %w; stderr=%s", p.Config.Name, err, stderr.String())
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
		return wire.SelectResponse{}, fmt.Errorf("%s: invalid select response JSON: %w", p.Config.Name, err)
	}
	if parsed.Success != nil {
		resp.Success = *parsed.Success
	}
	resp.Message = parsed.Message
	return resp, nil
}

// escapeJSONString escapes a string for embedding into JSON literals.
func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}
