package daemon

import (
	"bufio"
	"context"
	"log"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/iMithrellas/tarragon/internal/wire"
)

// pluginRegistry tracks connected plugins by name.
type pluginRegistry struct {
	mu       sync.RWMutex
	conns    map[string]net.Conn
	scanners map[string]*bufio.Scanner
}

func (r *pluginRegistry) set(name string, conn net.Conn, scanner *bufio.Scanner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns == nil {
		r.conns = make(map[string]net.Conn)
	}
	if r.scanners == nil {
		r.scanners = make(map[string]*bufio.Scanner)
	}
	if old, ok := r.conns[name]; ok {
		_ = old.Close()
	}
	r.conns[name] = conn
	r.scanners[name] = scanner
}

func (r *pluginRegistry) remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns != nil {
		if c, ok := r.conns[name]; ok {
			_ = c.Close()
		}
		delete(r.conns, name)
	}
	if r.scanners != nil {
		delete(r.scanners, name)
	}
}

func (r *pluginRegistry) isConnected(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.conns[name]
	return ok
}

// Names returns a sorted list of currently connected plugin names.
func (r *pluginRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.conns))
	for name := range r.conns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *pluginRegistry) getConn(name string) (net.Conn, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.conns[name]
	return c, ok
}

// Requests routed to plugins.
type pluginRequest struct {
	name    string
	queryID string
	text    string
	msgType string
}

func startPluginListener(ctx context.Context, store *aggregateStore, ui *uiRegistry) (chan<- pluginRequest, *pluginRegistry) {
	ln, err := wire.ListenUnix(wire.SocketPlugins)
	if err != nil {
		log.Fatalf("[PLUGINS] Failed to listen on %s: %v", wire.SocketPlugins, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = wire.CleanupSocket(wire.SocketPlugins)
	}()

	log.Printf("[PLUGINS] listening on %s", wire.SocketPlugins)
	registry := &pluginRegistry{conns: make(map[string]net.Conn), scanners: make(map[string]*bufio.Scanner)}

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

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[PLUGINS] accept error: %v", err)
				continue
			}
			go handlePluginConn(ctx, conn, registry, store, ui, elapsedMs)
		}
	}()

	reqChan := make(chan pluginRequest, 256)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-reqChan:
				conn, ok := registry.getConn(req.name)
				if !ok {
					log.Printf("[PLUGINS] drop request: plugin %s not connected", req.name)
					continue
				}
				msgType := req.msgType
				if msgType == "" {
					msgType = wire.MsgRequest
				}
				_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				err := wire.WriteMsg(conn, &wire.PluginRequest{Type: msgType, QueryID: req.queryID, Text: req.text})
				_ = conn.SetWriteDeadline(time.Time{})
				if err != nil {
					log.Printf("[PLUGINS] send to %s failed: %v", req.name, err)
					registry.remove(req.name)
					continue
				}
				startTiming(req.queryID, req.name)
			}
		}
	}()

	return reqChan, registry
}

func handlePluginConn(ctx context.Context, conn net.Conn, registry *pluginRegistry, store *aggregateStore, ui *uiRegistry, elapsedMs func(qid, plugin string) float64) {
	scanner := wire.NewScanner(conn)
	var hello wire.PluginHello
	if err := wire.ReadMsg(scanner, &hello); err != nil {
		log.Printf("[PLUGINS] failed hello read: %v", err)
		_ = conn.Close()
		return
	}
	if hello.Type != wire.MsgHello || hello.Name == "" {
		log.Printf("[PLUGINS] invalid hello: type=%q name=%q", hello.Type, hello.Name)
		_ = conn.Close()
		return
	}

	registry.set(hello.Name, conn, scanner)
	log.Printf("[PLUGINS] plugin connected: %s", hello.Name)

	defer func() {
		registry.remove(hello.Name)
		log.Printf("[PLUGINS] plugin disconnected: %s", hello.Name)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var resp wire.PluginResponse
		if err := wire.ReadMsg(scanner, &resp); err != nil {
			return
		}
		if resp.Type != wire.MsgResponse || resp.QueryID == "" {
			continue
		}
		snap, ok := store.update(resp.QueryID, hello.Name, elapsedMs(resp.QueryID, hello.Name), resp.Data)
		if ok {
			ui.publish(&wire.UpdateMessage{Type: "update", QueryID: resp.QueryID, Payload: snap})
		}
	}
}
