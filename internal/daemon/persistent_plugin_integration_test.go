package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

func TestPersistentPluginE2E(t *testing.T) {
	_ = wire.CleanupSocket(wire.SocketPlugins)
	_ = wire.CleanupSocket(wire.SocketUI)
	defer func() {
		_ = wire.CleanupSocket(wire.SocketPlugins)
		_ = wire.CleanupSocket(wire.SocketUI)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut, plugReg := startPluginListener(ctx, store, uiReg)

	const pluginName = "plug_persistent"
	mgr := plugins.NewManager("-")
	mgr.Plugins[pluginName] = &plugins.Plugin{Config: plugins.PluginConfig{
		Name:            pluginName,
		Enabled:         true,
		Lifecycle:       plugins.LifecycleDaemon,
		ProvidesGeneral: true,
		RequirePrefix:   false,
	}}

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, nil)

	pluginReqCh := make(chan wire.PluginRequest, 1)
	pluginErrCh := make(chan error, 1)
	go func() {
		conn, err := dialUnixWithRetry(wire.SocketPlugins, 3*time.Second)
		if err != nil {
			pluginErrCh <- err
			return
		}
		defer func() { _ = conn.Close() }()

		if err := wire.WriteMsg(conn, &wire.PluginHello{Type: wire.MsgHello, Name: pluginName}); err != nil {
			pluginErrCh <- err
			return
		}

		scanner := wire.NewScanner(conn)
		var req wire.PluginRequest
		if err := wire.ReadMsg(scanner, &req); err != nil {
			pluginErrCh <- err
			return
		}
		pluginReqCh <- req

		if err := wire.WriteMsg(conn, &wire.PluginResponse{
			Type:    wire.MsgResponse,
			QueryID: req.QueryID,
			Data:    json.RawMessage(`{"ok":true,"source":"persistent"}`),
		}); err != nil {
			pluginErrCh <- err
			return
		}
	}()

	uiConn, err := dialUnixWithRetry(wire.SocketUI, 3*time.Second)
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = uiConn.Close() }()

	if err := wire.WriteMsg(uiConn, &wire.UIRequest{Type: "query", ClientID: "ui-e2e", Text: "hello world"}); err != nil {
		t.Fatalf("write ui query: %v", err)
	}

	uiScanner := wire.NewScanner(uiConn)
	var ack wire.AckMessage
	if err := wire.ReadMsg(uiScanner, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Type != "ack" || ack.QueryID == "" {
		t.Fatalf("unexpected ack: %+v", ack)
	}

	select {
	case err := <-pluginErrCh:
		t.Fatalf("plugin flow error: %v", err)
	case req := <-pluginReqCh:
		if req.Type != wire.MsgRequest {
			t.Fatalf("unexpected plugin request type: %q", req.Type)
		}
		if req.QueryID != ack.QueryID {
			t.Fatalf("plugin request query id mismatch: got=%q want=%q", req.QueryID, ack.QueryID)
		}
		if req.Text != "hello world" {
			t.Fatalf("plugin request text mismatch: got=%q", req.Text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for plugin request")
	}

	var upd wire.UpdateMessage
	if err := wire.ReadMsg(uiScanner, &upd); err != nil {
		t.Fatalf("read update: %v", err)
	}
	if upd.Type != "update" || upd.QueryID != ack.QueryID {
		t.Fatalf("unexpected update header: %+v", upd)
	}

	var snap struct {
		QueryID string `json:"query_id"`
		Input   string `json:"input"`
		Results map[string]struct {
			Data json.RawMessage `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(upd.Payload, &snap); err != nil {
		t.Fatalf("unmarshal update payload: %v", err)
	}
	if snap.QueryID != ack.QueryID {
		t.Fatalf("snapshot query mismatch: got=%q want=%q", snap.QueryID, ack.QueryID)
	}
	if snap.Input != "hello world" {
		t.Fatalf("snapshot input mismatch: got=%q", snap.Input)
	}
	res, ok := snap.Results[pluginName]
	if !ok {
		t.Fatalf("missing plugin result for %s", pluginName)
	}
	if string(res.Data) != `{"ok":true,"source":"persistent"}` {
		t.Fatalf("unexpected plugin data: %s", string(res.Data))
	}
}

func dialUnixWithRetry(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, err
}
