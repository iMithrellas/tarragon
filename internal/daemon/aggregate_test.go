package daemon

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/iMithrellas/tarragon/internal/wire"
)

func TestAggregateStoreCreateUpdateSnapshot(t *testing.T) {
	s := newAggregateStore(10, "global", nil, 0.3)
	s.create("q1", "cli1", "hello")
	snap, ok := s.update("q1", "pluginA", 12.5, json.RawMessage(`{"v":1}`))
	if !ok {
		t.Fatalf("expected update ok")
	}
	var ag aggregate
	if err := json.Unmarshal(snap, &ag); err != nil {
		t.Fatalf("unmarshal snap: %v", err)
	}
	if ag.QueryID != "q1" || ag.Input != "hello" {
		t.Fatalf("unexpected snapshot header: %+v", ag)
	}
	r, exists := ag.Results["pluginA"]
	if !exists || r.ElapsedMs <= 0 || string(r.Data) != `{"v":1}` {
		t.Fatalf("unexpected result: %+v", ag.Results)
	}
}

func TestAggregateStoreEvictsOldest(t *testing.T) {
	s := newAggregateStore(2, "global", nil, 0.3)
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
	s := newAggregateStore(10, "global", nil, 0.3)
	s.create("q1", "cliA", "x")
	s.create("q2", "cliA", "y")
	s.create("q3", "cliB", "z")
	n := s.removeByClient("cliA")
	if n != 2 {
		t.Fatalf("expected removed 2, got %d", n)
	}
	if _, ok := s.update("q1", "p", 1, json.RawMessage(`{}`)); ok {
		t.Fatalf("q1 should be gone")
	}
	if _, ok := s.update("q3", "p", 1, json.RawMessage(`{}`)); !ok {
		t.Fatalf("q3 should remain")
	}
}

func TestOrderResultsGlobalSortsByScoreDescending(t *testing.T) {
	items := []wire.ResultItem{
		{ID: "a", Plugin: "p1", Score: 0.2},
		{ID: "b", Plugin: "p2", Score: 0.9},
		{ID: "c", Plugin: "p3", Score: 0.5},
		{ID: "d", Plugin: "p4", Score: 0.5},
	}

	got := orderResults(items, "global", nil, 0.3)
	gotIDs := []string{got[0].ID, got[1].ID, got[2].ID, got[3].ID}
	wantIDs := []string{"b", "c", "d", "a"}

	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("unexpected order: got=%v want=%v", gotIDs, wantIDs)
	}
}

func TestOrderResultsGroupedSortsGroupsByBestScore(t *testing.T) {
	items := []wire.ResultItem{
		{ID: "a1", Plugin: "A", Score: 0.2},
		{ID: "a2", Plugin: "A", Score: 0.8},
		{ID: "b1", Plugin: "B", Score: 0.9},
		{ID: "b2", Plugin: "B", Score: 0.1},
		{ID: "c1", Plugin: "C", Score: 0.3},
	}

	got := orderResults(items, "grouped", nil, 0.3)
	gotIDs := []string{got[0].ID, got[1].ID, got[2].ID, got[3].ID, got[4].ID}
	wantIDs := []string{"b1", "b2", "a1", "a2", "c1"}

	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("unexpected grouped order: got=%v want=%v", gotIDs, wantIDs)
	}
}

func TestAggregateStoreUpdateUsesConfiguredOrderingMode(t *testing.T) {
	s := newAggregateStore(10, "grouped", nil, 0.3)
	s.create("q1", "cli", "hello")

	_, ok := s.update("q1", "alpha", 1, json.RawMessage(`{
		"items": [
			{"id":"a1","label":"A1","score":0.7},
			{"id":"a2","label":"A2","score":0.6}
		]
	}`))
	if !ok {
		t.Fatalf("first update failed")
	}

	snap, ok := s.update("q1", "beta", 1, json.RawMessage(`{
		"items": [
			{"id":"b1","label":"B1","score":0.95},
			{"id":"b2","label":"B2","score":0.1}
		]
	}`))
	if !ok {
		t.Fatalf("second update failed")
	}

	var ag aggregate
	if err := json.Unmarshal(snap, &ag); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	gotIDs := []string{ag.List[0].ID, ag.List[1].ID, ag.List[2].ID, ag.List[3].ID}
	wantIDs := []string{"b1", "b2", "a1", "a2"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("unexpected list order: got=%v want=%v", gotIDs, wantIDs)
	}
}

func TestOrderResultsGlobalBlendsFrecencyAndSetsField(t *testing.T) {
	items := []wire.ResultItem{{ID: "id1", Plugin: "p", Score: 0.5}}
	scores := map[string]float64{"p:id1": 1.5}

	got := orderResults(items, "global", scores, 0.3)
	if len(got) != 1 {
		t.Fatalf("expected 1 item, got %d", len(got))
	}
	if got[0].FrecencyScore != 1.5 {
		t.Fatalf("unexpected frecency score: %v", got[0].FrecencyScore)
	}
	want := (1-0.3)*0.5 + 0.3*1.5
	if math.Abs(got[0].Score-want) > 1e-9 {
		t.Fatalf("unexpected blended score: got=%v want=%v", got[0].Score, want)
	}
}
