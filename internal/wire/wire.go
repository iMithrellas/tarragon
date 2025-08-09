package wire

import "encoding/json"

// Endpoints used across daemon and UI
const (
	EndpointUIReq   = "ipc:///tmp/tarragon-ui.ipc"      // REQ/REP for query initiation
	EndpointUISub   = "ipc:///tmp/tarragon-updates.ipc" // PUB/SUB for async updates
	EndpointPlugins = "ipc:///tmp/tarragon-plugins.ipc" // ROUTER for plugin workers
)

// Message type constants (for plugins)
const (
	MsgHello    = "hello"
	MsgRequest  = "request"
	MsgResponse = "response"
)

// UIRequest is sent from UI to daemon over REQ.
// Type can be "query" or "detach".
type UIRequest struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id,omitempty"`
	Text     string `json:"text,omitempty"`
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
