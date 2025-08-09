package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
	"github.com/iMithrellas/tarragon/internal/wire"
)

// Connected plugin registry (name -> routing id)
type pluginRegistry struct {
	mu       sync.RWMutex
	nameToID map[string][]byte
}

func (r *pluginRegistry) set(name string, id []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nameToID == nil {
		r.nameToID = make(map[string][]byte)
	}
	r.nameToID[name] = append([]byte(nil), id...)
}

func (r *pluginRegistry) isConnected(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nameToID[name]
	return ok
}

func (r *pluginRegistry) getID(name string) ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.nameToID[name]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), id...), true
}

// Requests routed to plugins
type pluginRequest struct {
	name    string
	queryID string
	text    string
}

// startPluginRouter starts a ROUTER socket for plugins and returns
// a request channel and a registry of connected plugins.
func startPluginRouter(ctx context.Context, store *aggregateStore) (chan<- pluginRequest, *pluginRegistry) {
	cleanupIPC(wire.EndpointPlugins)
	router := zmq4.NewRouter(ctx)
	if err := router.Listen(wire.EndpointPlugins); err != nil {
		log.Fatalf("[PLUGINS] Failed to bind ROUTER: %v", err)
	}
	log.Printf("[PLUGINS] ROUTER listening on %s", wire.EndpointPlugins)

	registry := &pluginRegistry{nameToID: make(map[string][]byte)}
	// Track per-plugin send times to compute response latency.
	type timingStore struct {
		mu sync.Mutex
		m  map[string]map[string]time.Time // queryID -> plugin -> start
	}
	ts := &timingStore{m: make(map[string]map[string]time.Time)}
	startTiming := func(qid, plugin string) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if ts.m[qid] == nil {
			ts.m[qid] = make(map[string]time.Time)
		}
		ts.m[qid][plugin] = time.Now()
	}
	elapsedMs := func(qid, plugin string) float64 {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if pm, ok := ts.m[qid]; ok {
			if t0, ok2 := pm[plugin]; ok2 {
				delete(pm, plugin)
				if len(pm) == 0 {
					delete(ts.m, qid)
				}
				return float64(time.Since(t0).Microseconds()) / 1000.0
			}
		}
		return 0
	}
	reqChan := make(chan pluginRequest, 256)

	// Sender goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-reqChan:
				id, ok := registry.getID(req.name)
				if !ok {
					log.Printf("[PLUGINS] drop request: plugin %s not connected", req.name)
					continue
				}
				body, _ := json.Marshal(&wire.PluginRequest{Type: wire.MsgRequest, QueryID: req.queryID, Text: req.text})
				if err := router.Send(zmq4.Msg{Frames: [][]byte{id, body}}); err != nil {
					log.Printf("[PLUGINS] send to %s failed: %v", req.name, err)
				} else {
					log.Printf("[PLUGINS] sent request to %s qid=%s", req.name, req.queryID)
					startTiming(req.queryID, req.name)
				}
			}
		}
	}()

	// Receiver goroutine
	go func() {
		for {
			msg, err := router.Recv()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[PLUGINS] recv error: %v", err)
				continue
			}
			if len(msg.Frames) < 2 {
				continue
			}
			id := msg.Frames[0]
			data := msg.Frames[1]
			var h map[string]json.RawMessage
			if err := json.Unmarshal(data, &h); err != nil {
				log.Printf("[PLUGINS] invalid JSON: %v", err)
				continue
			}
			var typ string
			_ = json.Unmarshal(h["type"], &typ)
			switch typ {
			case wire.MsgHello:
				var hello wire.PluginHello
				_ = json.Unmarshal(data, &hello)
				if hello.Name == "" {
					log.Printf("[PLUGINS] hello with empty name; ignoring")
					continue
				}
				registry.set(hello.Name, id)
				log.Printf("[PLUGINS] plugin connected: %s", hello.Name)
			case wire.MsgResponse:
				var resp wire.PluginResponse
				if err := json.Unmarshal(data, &resp); err != nil {
					log.Printf("[PLUGINS] invalid response json: %v", err)
					continue
				}
				if resp.QueryID == "" {
					log.Printf("[PLUGINS] response missing query_id")
					continue
				}
				// Resolve name from id (reverse lookup)
				name := ""
				registry.mu.RLock()
				for n, rid := range registry.nameToID {
					if bytes.Equal(rid, id) {
						name = n
						break
					}
				}
				registry.mu.RUnlock()
				// Update aggregate and publish full snapshot
				snap, ok := store.update(resp.QueryID, name, elapsedMs(resp.QueryID, name), resp.Data)
				if ok {
					upd := wire.UpdateMessage{Type: "update", QueryID: resp.QueryID, Payload: json.RawMessage(snap)}
					b, _ := json.Marshal(upd)
					enqueuePub(resp.QueryID, b)
				}
				log.Printf("[PLUGINS] response from %s qid=%s", name, resp.QueryID)
			default:
				log.Printf("[PLUGINS] unknown message type: %s", typ)
			}
		}
	}()

	return reqChan, registry
}
