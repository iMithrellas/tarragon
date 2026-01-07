package daemon

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/iMithrellas/tarragon/internal/wire"
)

// aggregate store keeps full results per query for streaming snapshots
type aggResult struct {
	ElapsedMs float64         `json:"elapsed_ms"`
	Data      json.RawMessage `json:"data"`
}

type aggregate struct {
	QueryID string               `json:"query_id"`
	Input   string               `json:"input"`
	Results map[string]aggResult `json:"results"`
	List    []wire.ResultItem    `json:"list"`
	Client  string               `json:"-"`
	Created time.Time            `json:"-"`
}

type aggregateStore struct {
	mu       sync.Mutex
	limit    int
	order    []string
	byID     map[string]*aggregate
	byClient map[string]map[string]struct{}
}

func newAggregateStore(limit int) *aggregateStore {
	return &aggregateStore{
		limit:    limit,
		byID:     make(map[string]*aggregate),
		byClient: make(map[string]map[string]struct{}),
	}
}

func (s *aggregateStore) create(qid, client, input string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[qid]; ok {
		return
	}
	if s.limit > 0 && len(s.byID) >= s.limit {
		// evict oldest
		oldest := s.order[0]
		s.order = s.order[1:]
		if ag, ok := s.byID[oldest]; ok {
			delete(s.byID, oldest)
			if set, ok := s.byClient[ag.Client]; ok {
				delete(set, oldest)
				if len(set) == 0 {
					delete(s.byClient, ag.Client)
				}
			}
		}
	}
	s.byID[qid] = &aggregate{
		QueryID: qid,
		Input:   input,
		Results: make(map[string]aggResult),
		Client:  client,
		Created: time.Now(),
	}
	s.order = append(s.order, qid)
	if s.byClient[client] == nil {
		s.byClient[client] = make(map[string]struct{})
	}
	s.byClient[client][qid] = struct{}{}
}

// update returns a snapshot JSON after applying the update.
func (s *aggregateStore) update(qid, plugin string, elapsed float64, data json.RawMessage) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ag, ok := s.byID[qid]
	if !ok {
		return nil, false
	}
	ag.Results[plugin] = aggResult{ElapsedMs: elapsed, Data: data}
	ag.List = orderResults(flattenResults(ag.Results))
	snap, _ := json.Marshal(ag)
	return snap, true
}

func flattenResults(results map[string]aggResult) []wire.ResultItem {
	out := make([]wire.ResultItem, 0)
	for name, res := range results {
		out = append(out, normalizeResults(name, res.Data)...)
	}
	return out
}

func normalizeResults(plugin string, raw json.RawMessage) []wire.ResultItem {
	if len(raw) == 0 {
		return nil
	}
	// Object with known array fields.
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		for _, k := range []string{"suggestions", "variants", "items", "choices", "results"} {
			if arr, ok := obj[k]; ok {
				if items := parseArrayItems(plugin, arr); len(items) > 0 {
					return items
				}
			}
		}
		// Single object with id/label/title/text.
		if item, ok := parseObjectItem(plugin, obj); ok {
			return []wire.ResultItem{item}
		}
	}
	// Top-level array.
	if items := parseArrayItems(plugin, raw); len(items) > 0 {
		return items
	}
	return nil
}

func parseArrayItems(plugin string, raw json.RawMessage) []wire.ResultItem {
	// Array of objects.
	var objs []map[string]any
	if json.Unmarshal(raw, &objs) == nil && len(objs) > 0 {
		out := make([]wire.ResultItem, 0, len(objs))
		for _, o := range objs {
			if item, ok := parseObjectItem(plugin, toRawMap(o)); ok {
				out = append(out, item)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// Array of strings.
	var strs []string
	if json.Unmarshal(raw, &strs) == nil && len(strs) > 0 {
		out := make([]wire.ResultItem, 0, len(strs))
		for _, s := range strs {
			out = append(out, wire.ResultItem{ID: s, Label: s, Plugin: plugin})
		}
		return out
	}
	return nil
}

func parseObjectItem(plugin string, obj map[string]json.RawMessage) (wire.ResultItem, bool) {
	id := readStringField(obj, "id")
	label := readStringField(obj, "label")
	if label == "" {
		label = readStringField(obj, "title")
	}
	if label == "" {
		label = readStringField(obj, "text")
	}
	if id == "" && label == "" {
		return wire.ResultItem{}, false
	}
	if id == "" {
		id = label
	}
	return wire.ResultItem{ID: id, Label: label, Plugin: plugin}, true
}

func readStringField(obj map[string]json.RawMessage, key string) string {
	raw, ok := obj[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func toRawMap(m map[string]any) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		if b, err := json.Marshal(v); err == nil {
			out[k] = b
		}
	}
	return out
}

func orderResults(items []wire.ResultItem) []wire.ResultItem {
	// TODO: apply scoring + sorting rules
	return items
}

func (s *aggregateStore) removeByClient(client string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.byClient[client]
	if set == nil {
		return 0
	}
	removed := 0
	for qid := range set {
		delete(s.byID, qid)
		removed++
		// remove from order
		for i, id := range s.order {
			if id == qid {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	delete(s.byClient, client)
	return removed
}
