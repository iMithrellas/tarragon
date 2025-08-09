// internal/daemon/daemon.go
package daemon

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "os"
    "os/exec"
    "os/signal"
    "path/filepath"
    "sync"
    "sync/atomic"
    "syscall"
    "time"

    "github.com/go-zeromq/zmq4"
    "github.com/iMithrellas/tarragon/internal/plugins"
    "github.com/iMithrellas/tarragon/pkg/models"
    "github.com/spf13/viper"
)

const (
    ipcEndpointUiReq   = "ipc:///tmp/tarragon-ui.ipc"       // REQ/REP for query initiation
    ipcEndpointUiPub   = "ipc:///tmp/tarragon-updates.ipc"  // PUB for async incremental updates
    ipcEndpointPlugins = "ipc:///tmp/tarragon-plugins.ipc"  // ROUTER for plugin workers (future)
)

var querySeq uint64

// message types for UI channel
type ackMessage struct {
    Type    string `json:"type"`
    QueryID string `json:"query_id"`
}

type updateMessage struct {
    Type    string          `json:"type"`
    QueryID string          `json:"query_id"`
    Payload json.RawMessage `json:"payload"`
    Done    bool            `json:"done"`
}

func RunDaemon() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    pluginDir := plugins.DefaultDir()
    mgr := plugins.NewManager(pluginDir)
    if err := mgr.Discover(); err != nil {
        log.Printf("Plugin discovery error: %v", err)
    }
    if err := mgr.StartPersistent(ctx, ipcEndpointPlugins); err != nil {
        log.Printf("Plugin start error: %v", err)
    }
    defer mgr.StopAll()

    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigs
        log.Println("Shutdown signal received.")
        cancel()
    }()

    // Start plugin ROUTER and get channels/registry
    reqOut, registry := startPluginRouter(ctx)

    // Start IPC server(s)
    if viper.GetBool("run_tcp") {
        go reqServer(ctx, "tcp://127.0.0.1:"+viper.GetString("port"), "TCP", mgr, reqOut, registry)
    }
    if viper.GetBool("run_ipc") {
        go reqServer(ctx, ipcEndpointUiReq, "IPC", mgr, reqOut, registry)
    }

    <-ctx.Done()
    log.Println("Daemon shutting down.")
}

// Remove stale ipc files to avoid EADDRINUSE on restart.
func cleanupIPC(endpoint string) {
    if len(endpoint) >= 6 && endpoint[:6] == "ipc://" {
        path := endpoint[6:]
        _ = os.MkdirAll(filepath.Dir(path), 0o755)
        if _, err := os.Stat(path); err == nil {
            os.Remove(path)
        }
    }
}

// reqServer handles UI REQ/REP and spawns goroutines to publish updates.
type pubFrame struct {
    topic   string
    payload []byte
}

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

// Global publisher enqueue channel
var pubEnqueue chan pubFrame

func enqueuePub(topic string, payload []byte) {
    if pubEnqueue != nil {
        pubEnqueue <- pubFrame{topic: topic, payload: payload}
    }
}

// startPluginRouter starts a ROUTER socket for plugins and returns
// a request channel and a registry of connected plugins.
func startPluginRouter(ctx context.Context) (chan<- pluginRequest, *pluginRegistry) {
    cleanupIPC(ipcEndpointPlugins)
    router := zmq4.NewRouter(ctx)
    if err := router.Listen(ipcEndpointPlugins); err != nil {
        log.Fatalf("[PLUGINS] Failed to bind ROUTER: %v", err)
    }
    log.Printf("[PLUGINS] ROUTER listening on %s", ipcEndpointPlugins)

    registry := &pluginRegistry{nameToID: make(map[string][]byte)}
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
                body, _ := json.Marshal(map[string]any{
                    "type":     "request",
                    "query_id": req.queryID,
                    "text":     req.text,
                })
                if err := router.Send(zmq4.Msg{Frames: [][]byte{id, body}}); err != nil {
                    log.Printf("[PLUGINS] send to %s failed: %v", req.name, err)
                } else {
                    log.Printf("[PLUGINS] sent request to %s qid=%s", req.name, req.queryID)
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
            var h struct {
                Type    string          `json:"type"`
                Name    string          `json:"name"`
                QueryID string          `json:"query_id"`
                Data    json.RawMessage `json:"data"`
            }
            if err := json.Unmarshal(data, &h); err != nil {
                log.Printf("[PLUGINS] invalid JSON: %v", err)
                continue
            }
            switch h.Type {
            case "hello":
                if h.Name == "" {
                    log.Printf("[PLUGINS] hello with empty name; ignoring")
                    continue
                }
                registry.set(h.Name, id)
                log.Printf("[PLUGINS] plugin connected: %s", h.Name)
            case "response":
                if h.QueryID == "" {
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
                env := struct {
                    Plugin string          `json:"plugin"`
                    Data   json.RawMessage `json:"data"`
                }{Plugin: name, Data: h.Data}
                pbytes, _ := json.Marshal(env)
                upd := updateMessage{Type: "update", QueryID: h.QueryID, Payload: pbytes, Done: false}
                b, _ := json.Marshal(upd)
                enqueuePub(h.QueryID, b)
                log.Printf("[PLUGINS] response from %s qid=%s", name, h.QueryID)
            default:
                log.Printf("[PLUGINS] unknown message type: %s", h.Type)
            }
        }
    }()

    return reqChan, registry
}

func reqServer(ctx context.Context, endpoint, label string, mgr *plugins.Manager, reqOut chan<- pluginRequest, registry *pluginRegistry) {
    cleanupIPC(endpoint)
    // Prepare PUB for updates on IPC by default. When using TCP, still publish on IPC updates endpoint.
    cleanupIPC(ipcEndpointUiPub)

    pub := zmq4.NewPub(ctx)
    if err := pub.Listen(ipcEndpointUiPub); err != nil {
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
    log.Printf("[%s] Listening REQ on %s; PUB on %s", label, endpoint, ipcEndpointUiPub)

    for {
        msg, err := rep.Recv()
        if err != nil {
            log.Printf("[%s] Error receiving: %v", label, err)
            continue
        }
        if len(msg.Frames) == 0 {
            continue
        }
        input := string(msg.Frames[0])
        qid := fmt.Sprintf("q-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&querySeq, 1))

        // Send ACK with query ID.
        ack := ackMessage{Type: "ack", QueryID: qid}
        ackBytes, _ := json.Marshal(ack)
        if err := rep.Send(zmq4.Msg{Frames: [][]byte{ackBytes}}); err != nil {
            log.Printf("[%s] Error sending ack: %v", label, err)
            continue
        }

        // Spawn a goroutine to route the query to connected plugins via ZMQ,
        // and publish a done marker after a deadline.
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
                    pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
                    raw, err := invokeOnCall(pctx, p, q)
                    cancel()
                    if err != nil {
                        raw = json.RawMessage([]byte(fmt.Sprintf(`{"error":"%s"}`, escapeJSONString(err.Error()))))
                    }
                    env := struct {
                        Plugin string          `json:"plugin"`
                        Data   json.RawMessage `json:"data"`
                    }{Plugin: name, Data: raw}
                    pbytes, _ := json.Marshal(env)
                    upd := updateMessage{Type: "update", QueryID: id, Payload: pbytes, Done: false}
                    b, _ := json.Marshal(upd)
                    pubChan <- pubFrame{topic: id, payload: b}
                }
            }

            // Send done after a fixed deadline; responses stream independently.
            time.Sleep(1500 * time.Millisecond)
            done := updateMessage{Type: "update", QueryID: id, Payload: json.RawMessage(`{}`), Done: true}
            db, _ := json.Marshal(done)
            pubChan <- pubFrame{topic: id, payload: db}
        }(input, qid)
    }
}

// lookupPayload searches the trie for the given key (exact match) and returns the corresponding payload.
func lookupPayload(trie *models.Trie, key string) *models.Payload {
    current := trie.Root
    for _, r := range key {
        node, ok := current.Children[r]
        if !ok {
            return nil
        }
        current = node
    }
    return current.Value
}

// invokeOnCall runs a plugin entrypoint once with the given query and returns its JSON output.
func invokeOnCall(ctx context.Context, p *plugins.Plugin, query string) (json.RawMessage, error) {
    entry := filepath.Join(p.Dir, p.Config.Entrypoint)
    cmd := exec.CommandContext(ctx, entry, "--once", query)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return nil, fmt.Errorf("%s: %w; stderr=%s", p.Config.Name, err, stderr.String())
    }
    out := bytes.TrimSpace(stdout.Bytes())
    if len(out) == 0 {
        out = []byte("{}")
    }
    return json.RawMessage(out), nil
}

// escapeJSONString escapes a string for embedding into JSON literals.
func escapeJSONString(s string) string {
    b, _ := json.Marshal(s)
    if len(b) >= 2 {
        return string(b[1 : len(b)-1])
    }
    return s
}
