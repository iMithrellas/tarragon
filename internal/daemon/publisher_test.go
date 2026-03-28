package daemon

import (
	"bufio"
	"net"
	"testing"

	"github.com/iMithrellas/tarragon/internal/wire"
)

func TestUIRegistryPublish(t *testing.T) {
	c1, c2 := net.Pipe()
	defer func() { _ = c2.Close() }()

	r := newUIRegistry()
	r.add(&uiClient{conn: c1, scanner: wire.NewScanner(c1), clientID: "u1"})

	done := make(chan *wire.UpdateMessage, 1)
	go func() {
		s := wire.NewScanner(bufio.NewReader(c2))
		var upd wire.UpdateMessage
		_ = wire.ReadMsg(s, &upd)
		done <- &upd
	}()

	r.publish(&wire.UpdateMessage{Type: "update", QueryID: "q1", Payload: []byte(`{"x":1}`)})
	upd := <-done
	if upd.Type != "update" || upd.QueryID != "q1" || string(upd.Payload) != `{"x":1}` {
		t.Fatalf("unexpected update: %+v", upd)
	}
}

func TestPublishToUINilRegistry(t *testing.T) {
	publishToUI(nil, "q1", []byte(`{}`))
}
