package daemon

import "testing"

func TestPluginRegistryBasic(t *testing.T) {
	r := &pluginRegistry{}
	if r.isConnected("p1") {
		t.Fatalf("unexpected connected before set")
	}
	r.set("p1", []byte{1, 2, 3})
	if !r.isConnected("p1") {
		t.Fatalf("expected connected")
	}
	id, ok := r.getID("p1")
	if !ok || len(id) != 3 || id[0] != 1 {
		t.Fatalf("unexpected id: %v %v", id, ok)
	}
}
