package wire

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

// Unix sockets used across daemon and UI.
const (
	SocketUI      = "/tmp/tarragon-ui.sock"
	SocketPlugins = "/tmp/tarragon-plugins.sock"

	// Backward-compatible aliases while parallel workstreams migrate.
	EndpointUIReq   = SocketUI
	EndpointUISub   = SocketUI
	EndpointPlugins = SocketPlugins
)

// Message type constants (for plugins)
const (
	MsgHello    = "hello"
	MsgRequest  = "request"
	MsgResponse = "response"
	MsgSelect   = "select"
)

// UIRequest is sent from UI to daemon over REQ.
// Type can be "query" or "detach".
type UIRequest struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id,omitempty"`
	Text     string `json:"text,omitempty"`
	// Optional fields for actions such as selection
	QueryID string `json:"query_id,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
}

// AckMessage is sent from daemon to UI after receiving a query.
type AckMessage struct {
	Type    string `json:"type"` // "ack"
	QueryID string `json:"query_id"`
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
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	Plugin string  `json:"plugin"`
	Score  float64 `json:"score,omitempty"`
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

// PluginResponse is sent from plugin to daemon.
type PluginResponse struct {
	Type    string          `json:"type"` // "response"
	QueryID string          `json:"query_id"`
	Data    json.RawMessage `json:"data"`
}

// NewScanner creates a line scanner suitable for NDJSON streams.
// Maximum line length is 1MB.
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	s.Buffer(buf, 1024*1024)
	return s
}

// WriteMsg writes one JSON message followed by '\n'.
func WriteMsg(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ReadMsg reads one NDJSON line and unmarshals it into v.
func ReadMsg(scanner *bufio.Scanner, v any) error {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	if err := json.Unmarshal(scanner.Bytes(), v); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	return nil
}

// CleanupSocket removes a stale unix socket path.
func CleanupSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListenUnix cleans up a stale path and starts a unix listener.
func ListenUnix(path string) (net.Listener, error) {
	if err := CleanupSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return ln, nil
}
