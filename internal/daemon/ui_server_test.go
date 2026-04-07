package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/iMithrellas/tarragon/internal/db"
	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

func TestUIServer_AckAndUpdateOverUDS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	plugName := "plug_oncall"
	entry := writeScript(t, dir, "once.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"query\" ]]; then echo '{\"ok\":true,\"data\":\"pong\"}'; fi\n")
	mgr := plugins.NewManager("-")
	mgr.Plugins[plugName] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: plugName, Entrypoint: filepath.Base(entry), Enabled: true, Lifecycle: plugins.LifecycleOnCall}}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, nil)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.SocketUI)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "query", ClientID: "cli-test", Text: "hello"}); err != nil {
		t.Fatalf("write query: %v", err)
	}

	var ack wire.AckMessage
	if err := wire.ReadMsg(scanner, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Type != "ack" || ack.QueryID == "" {
		t.Fatalf("bad ack: %+v", ack)
	}

	type aggView struct {
		QueryID string `json:"query_id"`
		Results map[string]struct {
			Data json.RawMessage `json:"data"`
		} `json:"results"`
		Input string `json:"input"`
	}

	var snapshot aggView
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var upd wire.UpdateMessage
		if err := wire.ReadMsg(scanner, &upd); err != nil {
			t.Fatalf("read update: %v", err)
		}
		if upd.Type != "update" || upd.QueryID != ack.QueryID {
			t.Fatalf("unexpected update header: %+v", upd)
		}
		if err := json.Unmarshal(upd.Payload, &snapshot); err != nil {
			t.Fatalf("unmarshal snapshot: %v", err)
		}
		if snapshot.Input != "hello" {
			t.Fatalf("unexpected input: %q", snapshot.Input)
		}
		if r, ok := snapshot.Results[plugName]; ok {
			if string(r.Data) != `{"ok":true,"data":"pong"}` {
				t.Fatalf("unexpected plugin data: %s", string(r.Data))
			}
			return
		}
	}
	t.Fatalf("missing plugin result for %s", plugName)
}

func TestUIServer_SelectAndDetachAck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := plugins.NewManager("-")
	dir := t.TempDir()
	database, openErr := db.Open(filepath.Join(dir, "frecency.db"))
	if openErr != nil {
		t.Fatalf("open db: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := newAggregateStore(10, "global", database, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	c1, c2 := net.Pipe()
	plugReg.set("plug_connected", c1, wire.NewScanner(c1))
	defer func() { _ = c2.Close() }()

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, database)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.SocketUI)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "select", ClientID: "cli-test", QueryID: "q-1", Plugin: "plug_connected", ID: "id-123", Action: "open"}); err != nil {
		t.Fatalf("write select: %v", err)
	}
	var okMsg map[string]any
	if err := wire.ReadMsg(scanner, &okMsg); err != nil {
		t.Fatalf("read select ack: %v", err)
	}
	if okMsg["type"] != "ok" {
		t.Fatalf("unexpected ack: %+v", okMsg)
	}

	select {
	case msg := <-reqOut:
		if msg.name != "plug_connected" || msg.queryID != "q-1" || msg.msgType != wire.MsgSelect || msg.resultID != "id-123" || msg.action != "open" {
			t.Fatalf("unexpected forwarded select: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected select forwarded")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		scores, serr := database.GetFrecencyScores(context.Background())
		if serr != nil {
			t.Fatalf("get frecency scores: %v", serr)
		}
		if scores["plug_connected:id-123"] > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := func() float64 {
		scores, _ := database.GetFrecencyScores(context.Background())
		return scores["plug_connected:id-123"]
	}(); got <= 0 {
		t.Fatalf("expected frecency score for recorded selection, got %v", got)
	}

	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "detach", ClientID: "cli-test"}); err != nil {
		t.Fatalf("write detach: %v", err)
	}
	if err := wire.ReadMsg(scanner, &okMsg); err != nil {
		t.Fatalf("read detach ack: %v", err)
	}
	if okMsg["type"] != "ok" {
		t.Fatalf("unexpected detach ack: %+v", okMsg)
	}
}

func TestUIServer_OnCallSelectInvokesCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	plugName := "plug_oncall_select"
	entry := writeScript(t, dir, "select.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"select\" && \"$3\" == \"img-42\" && \"$4\" == \"open\" ]]; then echo '{\"success\":true,\"message\":\"opened\"}'; exit 0; fi\nexit 1\n")
	mgr := plugins.NewManager("-")
	mgr.Plugins[plugName] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: plugName, Entrypoint: filepath.Base(entry), Enabled: true, Lifecycle: plugins.LifecycleOnCall}}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, nil)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.SocketUI)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "select", ClientID: "cli-test", Plugin: plugName, ResultID: "img-42", Action: "open"}); err != nil {
		t.Fatalf("write select: %v", err)
	}

	var msg map[string]json.RawMessage
	gotOK := false
	gotSelectResponse := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := wire.ReadMsg(scanner, &msg); err != nil {
			t.Fatalf("read message: %v", err)
		}
		var typ string
		if err := json.Unmarshal(msg["type"], &typ); err != nil {
			continue
		}
		switch typ {
		case "ok":
			gotOK = true
		case wire.MsgSelectResponse:
			var resp wire.SelectResponse
			b, _ := json.Marshal(msg)
			if err := json.Unmarshal(b, &resp); err != nil {
				t.Fatalf("unmarshal select response: %v", err)
			}
			if !resp.Success || resp.Message != "opened" {
				t.Fatalf("unexpected select response: %+v", resp)
			}
			gotSelectResponse = true
		}
		if gotOK && gotSelectResponse {
			break
		}
	}

	if !gotOK {
		t.Fatalf("expected immediate ok ack")
	}
	if !gotSelectResponse {
		t.Fatalf("expected select_response from on-call plugin invocation")
	}

	select {
	case forwarded := <-reqOut:
		t.Fatalf("did not expect forwarded select for on_call plugin: %+v", forwarded)
	default:
	}
}
