package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/go-zeromq/zmq4"
	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

var querySeq uint64

// reqServer handles UI REQ/REP and spawns goroutines to publish updates.
func reqServer(ctx context.Context, endpoint, label string, mgr *plugins.Manager, reqOut chan<- pluginRequest, registry *pluginRegistry, store *aggregateStore) {
	cleanupIPC(endpoint)
	// Prepare PUB for updates on IPC by default. When using TCP, still publish on IPC updates endpoint.
	cleanupIPC(wire.EndpointUISub)

	pub := zmq4.NewPub(ctx)
	if err := pub.Listen(wire.EndpointUISub); err != nil {
		log.Fatalf("[%s] Failed to bind PUB: %v", label, err)
	}
	defer pub.Close()

	// Single publisher goroutine to serialize socket access
	pubChan := make(chan pubFrame, 128)
	pubEnqueue = pubChan
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-pubChan:
				if err := pub.Send(zmq4.Msg{Frames: [][]byte{[]byte(m.topic), m.payload}}); err != nil {
					log.Printf("[%s] publish error: %v", label, err)
				}
			}
		}
	}()

	rep := zmq4.NewRep(ctx)
	if err := rep.Listen(endpoint); err != nil {
		log.Fatalf("[%s] Failed to bind REQ/REP: %v", label, err)
	}
	defer rep.Close()
	log.Printf("[%s] Listening REQ on %s; PUB on %s", label, endpoint, wire.EndpointUISub)

	for {
		msg, err := rep.Recv()
		if err != nil {
			log.Printf("[%s] Error receiving: %v", label, err)
			continue
		}
		if len(msg.Frames) == 0 {
			continue
		}
		// Parse incoming request: either raw text or JSON command
		var parsed wire.UIRequest
		body := msg.Frames[0]
		input := string(body)
		clientID := "default"
		if json.Unmarshal(body, &parsed) == nil && parsed.Type != "" {
			switch parsed.Type {
			case "query":
				if parsed.Text != "" {
					input = parsed.Text
				}
				if parsed.ClientID != "" {
					clientID = parsed.ClientID
				}
			case "detach":
				if parsed.ClientID != "" {
					n := store.removeByClient(parsed.ClientID)
					log.Printf("[%s] detach client=%s; cleared %d aggregates", label, parsed.ClientID, n)
				}
				// Acknowledge and continue
				ackBytes, _ := json.Marshal(map[string]any{"type": "ok"})
				_ = rep.Send(zmq4.Msg{Frames: [][]byte{ackBytes}})
				continue
			default:
				// unknown type -> fall through as raw input
			}
		}
		qid := fmt.Sprintf("q-%d-%d", time.Now().UnixNano(), atomicAdd(&querySeq, 1))

		// Send ACK with query ID.
		ack := wire.AckMessage{Type: "ack", QueryID: qid}
		ackBytes, _ := json.Marshal(ack)
		if err := rep.Send(zmq4.Msg{Frames: [][]byte{ackBytes}}); err != nil {
			log.Printf("[%s] Error sending ack: %v", label, err)
			continue
		}
		// Register aggregate for this query
		store.create(qid, clientID, input)

		// Spawn a goroutine to route the query to connected plugins via ZMQ.
		go func(q string, id string) {
			// Give subscribers time to attach after ACK (slow joiner mitigation).
			time.Sleep(250 * time.Millisecond)

			targets := 0
			for name, p := range mgr.Plugins {
				if !p.Config.Enabled {
					continue
				}
				if registry.isConnected(name) {
					reqOut <- pluginRequest{name: name, queryID: id, text: q}
					targets++
				}
			}

			// Fallback: execute on_call style if no connected plugins.
			if targets == 0 {
				for name, p := range mgr.Plugins {
					if !p.Config.Enabled {
						continue
					}
					t0 := time.Now()
					pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
					raw, err := invokeOnCall(pctx, p, q)
					cancel()
					if err != nil {
						raw = json.RawMessage([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeJSONString(err.Error()))))
					}
					if snap, ok := store.update(id, name, float64(time.Since(t0).Microseconds())/1000.0, raw); ok {
						upd := wire.UpdateMessage{Type: "update", QueryID: id, Payload: json.RawMessage(snap)}
						b, _ := json.Marshal(upd)
						pubChan <- pubFrame{topic: id, payload: b}
					}
				}
			}
		}(input, qid)
	}
}

// atomicAdd wraps sync/atomic.AddUint64 for clarity
func atomicAdd(ptr *uint64, delta uint64) uint64 { return atomic.AddUint64(ptr, delta) }
