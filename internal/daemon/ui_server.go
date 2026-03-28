package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iMithrellas/tarragon/internal/db"
	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

var querySeq uint64

// uiClient tracks a connected UI.
type uiClient struct {
	conn     net.Conn
	scanner  *bufio.Scanner
	clientID string
	mu       sync.Mutex // serialize writes
}

// uiRegistry tracks active UI clients.
type uiRegistry struct {
	mu      sync.RWMutex
	clients map[string]*uiClient
}

func newUIRegistry() *uiRegistry {
	return &uiRegistry{clients: make(map[string]*uiClient)}
}

func (r *uiRegistry) add(c *uiClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.clients[c.clientID]; ok && old != c {
		_ = old.conn.Close()
	}
	r.clients[c.clientID] = c
}

func (r *uiRegistry) updateClientID(c *uiClient, newID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, c.clientID)
	c.clientID = newID
	if old, ok := r.clients[newID]; ok && old != c {
		_ = old.conn.Close()
	}
	r.clients[newID] = c
}

func (r *uiRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[id]; ok {
		_ = c.conn.Close()
		delete(r.clients, id)
	}
}

func (r *uiRegistry) publish(msg *wire.UpdateMessage) {
	r.mu.RLock()
	clients := make([]*uiClient, 0, len(r.clients))
	ids := make([]string, 0, len(r.clients))
	for id, c := range r.clients {
		ids = append(ids, id)
		clients = append(clients, c)
	}
	r.mu.RUnlock()

	for i, c := range clients {
		c.mu.Lock()
		_ = c.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		err := wire.WriteMsg(c.conn, msg)
		_ = c.conn.SetWriteDeadline(time.Time{})
		c.mu.Unlock()
		if err != nil {
			r.remove(ids[i])
		}
	}
}

func startUIServer(ctx context.Context, mgr *plugins.Manager, reqOut chan<- pluginRequest, pluginsReg *pluginRegistry, store *aggregateStore, ui *uiRegistry, frecencyDB *db.DB) {
	ln, err := wire.ListenUnix(wire.SocketUI)
	if err != nil {
		log.Fatalf("[UI] failed to listen on %s: %v", wire.SocketUI, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = wire.CleanupSocket(wire.SocketUI)
	}()
	log.Printf("[UI] listening on %s", wire.SocketUI)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[UI] accept error: %v", err)
			continue
		}
		go handleUIClient(ctx, conn, mgr, reqOut, pluginsReg, store, ui, frecencyDB)
	}
}

func handleUIClient(ctx context.Context, conn net.Conn, mgr *plugins.Manager, reqOut chan<- pluginRequest, pluginsReg *pluginRegistry, store *aggregateStore, ui *uiRegistry, frecencyDB *db.DB) {
	scanner := wire.NewScanner(conn)
	uid := fmt.Sprintf("ui-%d", time.Now().UnixNano())
	client := &uiClient{conn: conn, scanner: scanner, clientID: uid}
	ui.add(client)
	defer ui.remove(uid)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var parsed wire.UIRequest
		if err := wire.ReadMsg(scanner, &parsed); err != nil {
			return
		}

		if parsed.ClientID != "" && parsed.ClientID != client.clientID {
			ui.updateClientID(client, parsed.ClientID)
		}

		switch parsed.Type {
		case "detach":
			targetClient := parsed.ClientID
			if targetClient == "" {
				targetClient = client.clientID
			}
			_ = store.removeByClient(targetClient)
			_ = wire.WriteMsg(conn, map[string]any{"type": "ok"})
			continue
		case "select":
			selectedID := parsed.ID
			if selectedID == "" {
				selectedID = parsed.Text
			}
			if frecencyDB != nil && parsed.Plugin != "" && selectedID != "" {
				go func(plugin, id string) {
					if err := frecencyDB.RecordSelection(context.Background(), plugin, id); err != nil {
						log.Printf("[UI] failed to record selection %s:%s: %v", plugin, id, err)
						return
					}
					if err := store.RefreshFrecencyCache(context.Background()); err != nil {
						log.Printf("[UI] failed to refresh frecency cache: %v", err)
					}
				}(parsed.Plugin, selectedID)
			}
			if parsed.Plugin != "" && parsed.QueryID != "" && pluginsReg.isConnected(parsed.Plugin) {
				reqOut <- pluginRequest{name: parsed.Plugin, queryID: parsed.QueryID, text: selectedID, msgType: wire.MsgSelect}
			}
			_ = wire.WriteMsg(conn, map[string]any{"type": "ok"})
			continue
		case "status":
			connected := pluginsReg.Names()
			total := 0
			for _, p := range mgr.Plugins {
				if p.Config.Enabled {
					total++
				}
			}
			_ = wire.WriteMsg(conn, &wire.StatusResponse{
				Type:      "status",
				Connected: connected,
				Total:     total,
			})
			continue
		case "query", "":
			// handled below
		default:
			_ = wire.WriteMsg(conn, map[string]any{"type": "error", "error": "unknown request type"})
			continue
		}

		input := parsed.Text
		if input == "" {
			continue
		}
		clientID := client.clientID
		if parsed.ClientID != "" {
			clientID = parsed.ClientID
		}

		qid := fmt.Sprintf("q-%d-%d", time.Now().UnixNano(), atomicAdd(&querySeq, 1))
		if err := wire.WriteMsg(conn, &wire.AckMessage{Type: "ack", QueryID: qid}); err != nil {
			return
		}

		store.create(qid, clientID, input)

		targetName, targetText, hasTarget := resolvePrefixTarget(input, mgr)
		if !hasTarget {
			targetText = input
		}

		go dispatchQuery(ctx, targetText, qid, mgr, reqOut, pluginsReg, store, ui, hasTarget, targetName)
	}
}

func dispatchQuery(ctx context.Context, queryText, qid string, mgr *plugins.Manager, reqOut chan<- pluginRequest, pluginsReg *pluginRegistry, store *aggregateStore, ui *uiRegistry, hasTarget bool, targetName string) {
	for name, p := range mgr.Plugins {
		if !p.Config.Enabled {
			continue
		}
		if p.Config.Lifecycle == plugins.LifecycleOnCall {
			continue
		}
		if p.Config.RequirePrefix && !hasTarget {
			continue
		}
		if hasTarget && name != targetName {
			continue
		}

		if p.Config.Lifecycle == plugins.LifecycleOnDemandPersistent && !pluginsReg.isConnected(name) {
			if err := mgr.StartOnDemand(ctx, name, wire.SocketPlugins); err != nil {
				log.Printf("[UI] failed to start on-demand plugin %s: %v", name, err)
				continue
			}

			connected := false
			for i := 0; i < 40; i++ {
				if pluginsReg.isConnected(name) {
					connected = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			if !connected {
				log.Printf("[UI] on-demand plugin %s did not connect within 2s; skipping query %s", name, qid)
				continue
			}
		}

		if pluginsReg.isConnected(name) {
			reqOut <- pluginRequest{name: name, queryID: qid, text: queryText}
		}
	}

	for name, p := range mgr.Plugins {
		if !p.Config.Enabled || p.Config.Lifecycle != plugins.LifecycleOnCall {
			continue
		}
		if p.Config.RequirePrefix && !hasTarget {
			continue
		}
		if hasTarget && name != targetName {
			continue
		}
		t0 := time.Now()
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		raw, err := invokeOnCall(pctx, p, queryText)
		cancel()
		if err != nil {
			raw = json.RawMessage([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeJSONString(err.Error()))))
		}
		if snap, ok := store.update(qid, name, float64(time.Since(t0).Microseconds())/1000.0, raw); ok {
			ui.publish(&wire.UpdateMessage{Type: "update", QueryID: qid, Payload: snap})
		}
	}
}

// atomicAdd wraps sync/atomic.AddUint64 for clarity.
func atomicAdd(ptr *uint64, delta uint64) uint64 { return atomic.AddUint64(ptr, delta) }

func resolvePrefixTarget(input string, mgr *plugins.Manager) (string, string, bool) {
	text := strings.TrimSpace(input)
	if text == "" {
		return "", "", false
	}
	var targetName string
	var targetPrefix string
	for name, p := range mgr.Plugins {
		if !p.Config.Enabled {
			continue
		}
		prefix := strings.TrimSpace(p.Config.Prefix)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(text, prefix) && len(prefix) > len(targetPrefix) {
			targetName = name
			targetPrefix = prefix
		}
	}
	if targetName == "" {
		return "", "", false
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(text, targetPrefix))
	return targetName, trimmed, true
}
