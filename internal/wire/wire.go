package wire

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
)

// Endpoints used across daemon and UI
const (
	SocketUI      = "/tmp/tarragon-ui.sock"
	SocketPlugins = "/tmp/tarragon-plugins.sock"
	// Compatibility aliases for parallel workstreams still using legacy names.
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

// WriteMsg marshals v as JSON and writes it followed by \n to w.
func WriteMsg(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// ReadMsg reads one JSON line from scanner and unmarshals into v.
func ReadMsg(scanner *bufio.Scanner, v any) error {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return json.Unmarshal(scanner.Bytes(), v)
}

// NewScanner creates a bufio.Scanner for NDJSON with 1MB max line size.
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return s
}

// CleanupSocket removes a stale Unix socket path if present.
func CleanupSocket(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListenUnix listens on a Unix domain socket after cleaning up any stale file.
func ListenUnix(path string) (net.Listener, error) {
	if err := CleanupSocket(path); err != nil {
		return nil, err
	}
	return net.Listen("unix", path)
}
