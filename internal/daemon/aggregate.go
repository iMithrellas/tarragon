package daemon

import (
	"encoding/json"
	"sync"
	"time"
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

func (s *aggregateStore) setLimit(limit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit = limit
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
	snap, _ := json.Marshal(ag)
	return snap, true
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
