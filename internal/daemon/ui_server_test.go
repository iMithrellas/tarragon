package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-zeromq/zmq4"
	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

// Test the UI API surface of reqServer: ACK on query, update publish (fallback path), and select forwarding.
func TestUIServer_AckAndSelectAndFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Prepare a temp plugin that supports --once and prints stable JSON
	dir := t.TempDir()
	plugName := "plug_oncall"
	entry := writeScript(t, dir, "once.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"--once\" ]]; then echo '{\"ok\":true,\"data\":\"pong\"}'; fi\n")
	mgr := plugins.NewManager("-")
	mgr.Plugins[plugName] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: plugName, Entrypoint: filepath.Base(entry), Enabled: true}}

	store := newAggregateStore(10)
	reqOut, registry := startPluginRouter(ctx, store) // start router for forwarding; we won't actually connect plugins here
	_ = reqOut

	// Start UI server on a unique REQ endpoint
	endpoint := fmt.Sprintf("ipc:///tmp/tarragon-ui-test-%d.ipc", time.Now().UnixNano())
	go reqServer(ctx, endpoint, "TEST", mgr, reqOut, registry, store)

	// Client sockets
	cliReq := zmq4.NewReq(ctx)
	if err := cliReq.Dial(endpoint); err != nil {
		t.Fatalf("dial req: %v", err)
	}
	defer func() { _ = cliReq.Close() }()
	cliSub := zmq4.NewSub(ctx)
	if err := cliSub.Dial(wire.EndpointUISub); err != nil {
		t.Fatalf("dial sub: %v", err)
	}
	defer func() { _ = cliSub.Close() }()

	// 1) Send a query and expect an ack with query_id
	body, _ := json.Marshal(&wire.UIRequest{Type: "query", ClientID: "cli-test", Text: "hello"})
	if err := cliReq.Send(zmq4.NewMsg(body)); err != nil {
		t.Fatalf("send query: %v", err)
	}
	ackMsg, err := cliReq.Recv()
	if err != nil || len(ackMsg.Frames) == 0 {
		t.Fatalf("recv ack: %v", err)
	}
	var ack wire.AckMessage
	if json.Unmarshal(ackMsg.Frames[0], &ack) != nil || ack.QueryID == "" {
		t.Fatalf("bad ack: %q", string(ackMsg.Frames[0]))
	}
	// subscribe to updates for this query
	_ = cliSub.SetOption(zmq4.OptionSubscribe, ack.QueryID)

	// 2) Expect a published update containing aggregate snapshot with our plugin result (fallback path)
	type aggView struct {
		QueryID string `json:"query_id"`
		Results map[string]struct {
			Data json.RawMessage `json:"data"`
		} `json:"results"`
		Input string `json:"input"`
	}
	// receive with timeout
	deadline := time.Now().Add(3 * time.Second)
	var snapshot aggView
	for time.Now().Before(deadline) {
		msg, err := cliSub.Recv()
		if err != nil || len(msg.Frames) < 2 {
			continue
		}
		var upd wire.UpdateMessage
		if json.Unmarshal(msg.Frames[1], &upd) != nil {
			continue
		}
		if upd.QueryID != ack.QueryID {
			continue
		}
		if json.Unmarshal(upd.Payload, &snapshot) == nil && snapshot.QueryID == ack.QueryID {
			break
		}
	}
	if snapshot.QueryID != ack.QueryID {
		t.Fatalf("no snapshot for %s", ack.QueryID)
	}
	if snapshot.Input != "hello" {
		t.Fatalf("unexpected input: %q", snapshot.Input)
	}
	if r, ok := snapshot.Results[plugName]; !ok {
		t.Fatalf("missing plugin result for %s", plugName)
	} else if string(r.Data) != `{"ok":true,"data":"pong"}` {
		t.Fatalf("unexpected plugin data: %s", string(r.Data))
	}

	// 3) Selection forwarding: mark a plugin connected and send a select
	registry.set("plug_connected", []byte{1})
	// Use a fresh REQ to avoid blocking
	selBody, _ := json.Marshal(&wire.UIRequest{Type: "select", ClientID: "cli-test", QueryID: ack.QueryID, Plugin: "plug_connected", Text: "id-123"})
	if err := cliReq.Send(zmq4.NewMsg(selBody)); err != nil {
		t.Fatalf("send select: %v", err)
	}
	if _, err := cliReq.Recv(); err != nil {
		t.Fatalf("recv select ack: %v", err)
	}
	// We can't easily observe reqOut beyond router internals; but absence of panic and ack OK covers the path.
}
