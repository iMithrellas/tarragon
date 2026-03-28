package daemon

import (
	"net"
	"testing"

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
