package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
)

// Unix socket paths used across daemon and clients.
const (
	SocketUI      = "/tmp/tarragon-ui.sock"
	SocketPlugins = "/tmp/tarragon-plugins.sock"
)

// Message type constants (for plugins)
const (
	MsgHello          = "hello"
	MsgRequest        = "request"
	MsgResponse       = "response"
	MsgSelect         = "select"
	MsgSelectResponse = "select_response"
	MsgStatus         = "status"
)

// Action describes a named action a plugin can perform on a result.
type Action struct {
	Name        string `json:"name"`
	Default     bool   `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// SelectResponse is sent from daemon to UI after executing a select action.
type SelectResponse struct {
	Type    string `json:"type"`    // "select_response"
	Success bool   `json:"success"` // whether the action succeeded
	Message string `json:"message,omitempty"`
}

// UIRequest is sent from UI to daemon over REQ.
// Type can be "query", "select", "detach", or "status".
type UIRequest struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id,omitempty"`
	Text     string `json:"text,omitempty"`
	// Optional fields for actions such as selection
	QueryID  string `json:"query_id,omitempty"`
	Plugin   string `json:"plugin,omitempty"`
	ResultID string `json:"result_id,omitempty"`
	Action   string `json:"action,omitempty"`
	ID       string `json:"id,omitempty"` // backward compatibility alias for ResultID
}

// AckMessage is sent from daemon to UI after receiving a query.
type AckMessage struct {
	Type    string `json:"type"` // "ack"
	QueryID string `json:"query_id"`
}

// PluginInfo describes a plugin's metadata and connection status for UI display.
type PluginInfo struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Enabled         bool     `json:"enabled"`
	Connected       bool     `json:"connected"`
	Lifecycle       string   `json:"lifecycle"`
	Prefix          string   `json:"prefix,omitempty"`
	RequirePrefix   bool     `json:"require_prefix,omitempty"`
	ProvidesGeneral bool     `json:"provides_general_suggestions,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Icon            string   `json:"icon,omitempty"`
}

// StatusResponse carries plugin connection status to the UI.
type StatusResponse struct {
	Type      string       `json:"type"`              // "status"
	Connected []string     `json:"connected"`         // sorted connected plugin names
	Total     int          `json:"total"`             // total enabled plugins
	Plugins   []PluginInfo `json:"plugins,omitempty"` // full metadata per plugin
}

// UpdateMessage is streamed from daemon to UI with aggregate snapshots.
type UpdateMessage struct {
	Type    string `json:"type"` // "update"
	QueryID string `json:"query_id"`
	// Payload is a JSON-encoded aggregate snapshot
	Payload []byte `json:"payload"`
}

// ResultItem represents a normalized suggestion for UI rendering.
type ResultItem struct {
	ID            string   `json:"id"`
	Label         string   `json:"label,omitempty"`
	Description   string   `json:"description,omitempty"`
	Icon          string   `json:"icon,omitempty"`
	Category      string   `json:"category,omitempty"`
	PreviewPath   string   `json:"preview_path,omitempty"`
	Plugin        string   `json:"plugin"`
	Score         float64  `json:"score,omitempty"`
	FrecencyScore float64  `json:"frecency_score,omitempty"`
	Actions       []Action `json:"actions,omitempty"`
}

// PluginHello identifies the plugin to the daemon router.
type PluginHello struct {
	Type string `json:"type"` // "hello"
	Name string `json:"name"`
}

// PluginRequest is sent from daemon to a plugin.
type PluginRequest struct {
	Type    string `json:"type"` // "request"
	QueryID string `json:"query_id"`
	Text    string `json:"text"`
}

// SelectRequest is sent from daemon to a plugin for action execution.
type SelectRequest struct {
	Type     string `json:"type"`
	Plugin   string `json:"plugin,omitempty"`
	ResultID string `json:"result_id"`
	Action   string `json:"action"`
	QueryID  string `json:"query_id"`
}

// PluginResponse is sent from plugin to daemon.
type PluginResponse struct {
	Type    string          `json:"type"` // "response"
	QueryID string          `json:"query_id"`
	Data    json.RawMessage `json:"data"`
}

const maxNDJSONLineSize = 1 << 20 // 1MB

// WriteMsg marshals v as JSON and writes it as a single NDJSON line.
//
// If multiple goroutines share the same connection/writer, callers must
// serialize access (for example with a connection-scoped sync.Mutex).
func WriteMsg(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}

	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// ReadMsg reads one NDJSON line from scanner and unmarshals it into v.
func ReadMsg(scanner *bufio.Scanner, v any) error {
	if scanner.Scan() {
		return json.Unmarshal(scanner.Bytes(), v)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

// NewScanner creates a scanner configured for NDJSON with 1MB max line size.
func NewScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxNDJSONLineSize)
	return scanner
}

// CleanupSocket removes a stale unix socket file if it exists.
func CleanupSocket(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", path)
	}

	return os.Remove(path)
}

// ListenUnix creates a unix listener after cleaning up stale socket files.
func ListenUnix(path string) (net.Listener, error) {
	if err := CleanupSocket(path); err != nil {
		return nil, err
	}
	return net.Listen("unix", path)
}
