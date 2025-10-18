package daemon

import (
    "encoding/json"
    "testing"
)

func TestAggregateStoreCreateUpdateSnapshot(t *testing.T) {
    s := newAggregateStore(10)
    s.create("q1", "cli1", "hello")
    snap, ok := s.update("q1", "pluginA", 12.5, json.RawMessage(`{"v":1}`))
    if !ok { t.Fatalf("expected update ok") }
    var ag aggregate
    if err := json.Unmarshal(snap, &ag); err != nil { t.Fatalf("unmarshal snap: %v", err) }
    if ag.QueryID != "q1" || ag.Input != "hello" {
        t.Fatalf("unexpected snapshot header: %+v", ag)
    }
    r, exists := ag.Results["pluginA"]
    if !exists || r.ElapsedMs <= 0 || string(r.Data) != `{"v":1}` {
        t.Fatalf("unexpected result: %+v", ag.Results)
    }
}

func TestAggregateStoreEvictsOldest(t *testing.T) {
    s := newAggregateStore(2)
    s.create("q1", "cli", "a")
    s.create("q2", "cli", "b")
    s.create("q3", "cli", "c") // should evict q1
    if _, ok := s.update("q1", "p", 1, json.RawMessage(`{}`)); ok {
        t.Fatalf("expected q1 evicted")
    }
    if _, ok := s.update("q2", "p", 1, json.RawMessage(`{}`)); !ok {
        t.Fatalf("expected q2 present")
    }
    if _, ok := s.update("q3", "p", 1, json.RawMessage(`{}`)); !ok {
        t.Fatalf("expected q3 present")
    }
}

func TestAggregateStoreRemoveByClient(t *testing.T) {
    s := newAggregateStore(10)
    s.create("q1", "cliA", "x")
    s.create("q2", "cliA", "y")
    s.create("q3", "cliB", "z")
    n := s.removeByClient("cliA")
    if n != 2 { t.Fatalf("expected removed 2, got %d", n) }
    if _, ok := s.update("q1", "p", 1, json.RawMessage(`{}`)); ok {
        t.Fatalf("q1 should be gone")
    }
    if _, ok := s.update("q3", "p", 1, json.RawMessage(`{}`)); !ok {
        t.Fatalf("q3 should remain")
    }
}

