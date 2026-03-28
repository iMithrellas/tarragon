package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/iMithrellas/tarragon/internal/wire"
)

func TestPluginRegistryBasic(t *testing.T) {
	r := &pluginRegistry{}
	if r.isConnected("p1") {
		t.Fatalf("unexpected connected before set")
	}
	c1, c2 := net.Pipe()
	defer func() { _ = c2.Close() }()
	s := wire.NewScanner(c1)
	r.set("p1", c1, s)
	if !r.isConnected("p1") {
		t.Fatalf("expected connected")
	}
	c, ok := r.getConn("p1")
	if !ok || c == nil {
		t.Fatalf("unexpected conn: %v %v", c, ok)
	}
	r.remove("p1")
	if r.isConnected("p1") {
		t.Fatalf("expected disconnected after remove")
	}
}

func TestHandlePluginConn_SelectResponseBroadcastsToUI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newAggregateStore(10, "global")
	uiReg := newUIRegistry()

	uiSrv, uiCli := net.Pipe()
	defer func() { _ = uiCli.Close() }()
	uiReg.add(&uiClient{conn: uiSrv, scanner: wire.NewScanner(uiSrv), clientID: "u1"})

	pluginSrv, pluginCli := net.Pipe()
	defer func() { _ = pluginCli.Close() }()

	registry := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	done := make(chan *wire.SelectResponse, 1)
	go func() {
		s := wire.NewScanner(uiCli)
		var resp wire.SelectResponse
		_ = wire.ReadMsg(s, &resp)
		done <- &resp
	}()

	go handlePluginConn(ctx, pluginSrv, registry, store, uiReg, func(string, string) float64 { return 0 })

	if err := wire.WriteMsg(pluginCli, &wire.PluginHello{Type: wire.MsgHello, Name: "plug1"}); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if err := wire.WriteMsg(pluginCli, &wire.SelectResponse{Type: wire.MsgSelectResponse, Success: true, Message: "executed"}); err != nil {
		t.Fatalf("write select_response: %v", err)
	}

	select {
	case resp := <-done:
		if resp.Type != wire.MsgSelectResponse || !resp.Success || resp.Message != "executed" {
			t.Fatalf("unexpected select response: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for select response")
	}
}

func TestPluginListener_SendSelectRequestShape(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newAggregateStore(10, "global")
	uiReg := newUIRegistry()
	reqOut, reg := startPluginListener(ctx, store, uiReg)

	conn, err := dialUnixWithRetry(wire.SocketPlugins, 2*time.Second)
	if err != nil {
		t.Fatalf("dial plugin socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := wire.WriteMsg(conn, &wire.PluginHello{Type: wire.MsgHello, Name: "plug_select"}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !reg.isConnected("plug_select") {
		if time.Now().After(deadline) {
			t.Fatal("plugin did not become connected in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	scanner := wire.NewScanner(conn)
	reqOut <- pluginRequest{name: "plug_select", queryID: "q-123", resultID: "r-42", action: "open", msgType: wire.MsgSelect}

	var raw map[string]json.RawMessage
	if err := wire.ReadMsg(scanner, &raw); err != nil {
		t.Fatalf("read select request: %v", err)
	}

	var typ, qid, rid, action string
	_ = json.Unmarshal(raw["type"], &typ)
	_ = json.Unmarshal(raw["query_id"], &qid)
	_ = json.Unmarshal(raw["result_id"], &rid)
	_ = json.Unmarshal(raw["action"], &action)

	if typ != wire.MsgSelect || qid != "q-123" || rid != "r-42" || action != "open" {
		t.Fatalf("unexpected select request payload: type=%q query_id=%q result_id=%q action=%q", typ, qid, rid, action)
	}
}
