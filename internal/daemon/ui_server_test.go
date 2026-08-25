package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mithrel-dots/tarragon/internal/db"
	"github.com/mithrel-dots/tarragon/internal/plugins"
	"github.com/mithrel-dots/tarragon/internal/wire"
	"github.com/spf13/viper"
)

func TestUIServer_AckAndUpdateOverUDS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	plugName := "plug_oncall"
	entry := writeScript(t, dir, "once.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"query\" ]]; then echo '{\"ok\":true,\"data\":\"pong\"}'; fi\n")
	mgr := plugins.NewManager("-")
	mgr.Plugins[plugName] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: plugName, Entrypoint: filepath.Base(entry), Enabled: true, Lifecycle: plugins.LifecycleOnCall, ProvidesGeneral: true}}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, nil)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.ResolveUISocketPath())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "query", ClientID: "cli-test", Text: "hello"}); err != nil {
		t.Fatalf("write query: %v", err)
	}

	var ack wire.AckMessage
	if err := wire.ReadMsg(scanner, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Type != "ack" || ack.QueryID == "" {
		t.Fatalf("bad ack: %+v", ack)
	}

	type aggView struct {
		QueryID string `json:"query_id"`
		Results map[string]struct {
			Data json.RawMessage `json:"data"`
		} `json:"results"`
		Input string `json:"input"`
	}

	var snapshot aggView
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var upd wire.UpdateMessage
		if err := wire.ReadMsg(scanner, &upd); err != nil {
			t.Fatalf("read update: %v", err)
		}
		if upd.Type != "update" || upd.QueryID != ack.QueryID {
			t.Fatalf("unexpected update header: %+v", upd)
		}
		if err := json.Unmarshal(upd.Payload, &snapshot); err != nil {
			t.Fatalf("unmarshal snapshot: %v", err)
		}
		if snapshot.Input != "hello" {
			t.Fatalf("unexpected input: %q", snapshot.Input)
		}
		if r, ok := snapshot.Results[plugName]; ok {
			if string(r.Data) != `{"ok":true,"data":"pong"}` {
				t.Fatalf("unexpected plugin data: %s", string(r.Data))
			}
			return
		}
	}
	t.Fatalf("missing plugin result for %s", plugName)
}

func TestUIServer_SelectAndDetachAck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := plugins.NewManager("-")
	dir := t.TempDir()
	database, openErr := db.Open(filepath.Join(dir, "frecency.db"))
	if openErr != nil {
		t.Fatalf("open db: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := newAggregateStore(10, "global", database, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	c1, c2 := net.Pipe()
	plugReg.set("plug_connected", c1, wire.NewScanner(c1))
	defer func() { _ = c2.Close() }()

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, database)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.ResolveUISocketPath())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "select", ClientID: "cli-test", QueryID: "q-1", Plugin: "plug_connected", ID: "id-123", Action: "open"}); err != nil {
		t.Fatalf("write select: %v", err)
	}
	var okMsg map[string]any
	if err := wire.ReadMsg(scanner, &okMsg); err != nil {
		t.Fatalf("read select ack: %v", err)
	}
	if okMsg["type"] != "ok" {
		t.Fatalf("unexpected ack: %+v", okMsg)
	}

	select {
	case msg := <-reqOut:
		if msg.name != "plug_connected" || msg.queryID != "q-1" || msg.msgType != wire.MsgSelect || msg.resultID != "id-123" || msg.action != "open" {
			t.Fatalf("unexpected forwarded select: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected select forwarded")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		scores, serr := database.GetFrecencyScores(context.Background())
		if serr != nil {
			t.Fatalf("get frecency scores: %v", serr)
		}
		if scores["plug_connected:id-123"] > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := func() float64 {
		scores, _ := database.GetFrecencyScores(context.Background())
		return scores["plug_connected:id-123"]
	}(); got <= 0 {
		t.Fatalf("expected frecency score for recorded selection, got %v", got)
	}

	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "detach", ClientID: "cli-test"}); err != nil {
		t.Fatalf("write detach: %v", err)
	}
	if err := wire.ReadMsg(scanner, &okMsg); err != nil {
		t.Fatalf("read detach ack: %v", err)
	}
	if okMsg["type"] != "ok" {
		t.Fatalf("unexpected detach ack: %+v", okMsg)
	}
}

func TestUIServer_StatusIncludesPluginSourceMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := plugins.NewManager("-")
	mgr.Plugins["sys_plugin"] = &plugins.Plugin{Config: plugins.PluginConfig{
		Name:      "sys_plugin",
		Source:    "system",
		Enabled:   true,
		Lifecycle: plugins.LifecycleOnCall,
	}}
	mgr.Plugins["legacy_plugin"] = &plugins.Plugin{Config: plugins.PluginConfig{
		Name:      "legacy_plugin",
		Enabled:   true,
		Lifecycle: plugins.LifecycleOnCall,
	}}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, nil)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.ResolveUISocketPath())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "status", ClientID: "cli-test"}); err != nil {
		t.Fatalf("write status: %v", err)
	}

	var status wire.StatusResponse
	if err := wire.ReadMsg(scanner, &status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status.Type != wire.MsgStatus {
		t.Fatalf("unexpected status type: %q", status.Type)
	}
	if status.Total != 2 {
		t.Fatalf("expected 2 enabled plugins, got %d", status.Total)
	}

	seen := map[string]wire.PluginInfo{}
	for _, info := range status.Plugins {
		seen[info.Name] = info
	}

	if got := seen["sys_plugin"].Source; got != "system" {
		t.Fatalf("expected sys_plugin source=system, got %q", got)
	}
	if got := seen["legacy_plugin"].Source; got != "" {
		t.Fatalf("expected legacy_plugin source empty, got %q", got)
	}
}

func TestUIServer_OnCallSelectInvokesCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	plugName := "plug_oncall_select"
	entry := writeScript(t, dir, "select.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"select\" && \"$3\" == \"img-42\" && \"$4\" == \"open\" ]]; then echo '{\"success\":true,\"message\":\"opened\"}'; exit 0; fi\nexit 1\n")
	mgr := plugins.NewManager("-")
	mgr.Plugins[plugName] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: plugName, Entrypoint: filepath.Base(entry), Enabled: true, Lifecycle: plugins.LifecycleOnCall}}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	go startUIServer(ctx, mgr, reqOut, plugReg, store, uiReg, nil)

	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", wire.ResolveUISocketPath())
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial ui socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	scanner := wire.NewScanner(conn)
	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "select", ClientID: "cli-test", Plugin: plugName, ResultID: "img-42", Action: "open"}); err != nil {
		t.Fatalf("write select: %v", err)
	}

	var msg map[string]json.RawMessage
	gotOK := false
	gotSelectResponse := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := wire.ReadMsg(scanner, &msg); err != nil {
			t.Fatalf("read message: %v", err)
		}
		var typ string
		if err := json.Unmarshal(msg["type"], &typ); err != nil {
			continue
		}
		switch typ {
		case "ok":
			gotOK = true
		case wire.MsgSelectResponse:
			var resp wire.SelectResponse
			b, _ := json.Marshal(msg)
			if err := json.Unmarshal(b, &resp); err != nil {
				t.Fatalf("unmarshal select response: %v", err)
			}
			if !resp.Success || resp.Message != "opened" {
				t.Fatalf("unexpected select response: %+v", resp)
			}
			gotSelectResponse = true
		}
		if gotOK && gotSelectResponse {
			break
		}
	}

	if !gotOK {
		t.Fatalf("expected immediate ok ack")
	}
	if !gotSelectResponse {
		t.Fatalf("expected select_response from on-call plugin invocation")
	}

	select {
	case forwarded := <-reqOut:
		t.Fatalf("did not expect forwarded select for on_call plugin: %+v", forwarded)
	default:
	}
}

func TestUIServer_ReloadDiscoversNewPlugins(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pluginRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[plugins]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	viper.SetConfigFile(configPath)

	mgr := plugins.NewManager(pluginRoot)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "existing"), 0o755); err != nil {
		t.Fatalf("mkdir existing plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "existing", "plugin.toml"), []byte(""+
		"name=\"existing\"\n"+
		"enabled=true\n"+
		"entrypoint=\"existing.sh\"\n"+
		"lifecycle_mode=\"on_call\"\n"), 0o644); err != nil {
		t.Fatalf("write existing manifest: %v", err)
	}
	if err := mgr.Discover(); err != nil {
		t.Fatalf("initial discover: %v", err)
	}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 8)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go handleUIClient(ctx, serverConn, mgr, reqOut, plugReg, store, uiReg, nil)

	if err := os.MkdirAll(filepath.Join(pluginRoot, "newplug"), 0o755); err != nil {
		t.Fatalf("mkdir new plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "newplug", "plugin.toml"), []byte(""+
		"name=\"newplug\"\n"+
		"enabled=true\n"+
		"entrypoint=\"new.sh\"\n"+
		"lifecycle_mode=\"on_call\"\n"), 0o644); err != nil {
		t.Fatalf("write new manifest: %v", err)
	}

	if err := wire.WriteMsg(clientConn, &wire.UIRequest{Type: "reload", ClientID: "cli-test"}); err != nil {
		t.Fatalf("write reload: %v", err)
	}

	scanner := wire.NewScanner(clientConn)
	var reloadResp wire.ReloadResponse
	if err := wire.ReadMsg(scanner, &reloadResp); err != nil {
		t.Fatalf("read reload response: %v", err)
	}
	if !reloadResp.Success {
		t.Fatalf("reload failed: %s", reloadResp.Message)
	}

	if _, ok := mgr.Plugins["newplug"]; !ok {
		t.Fatalf("expected new plugin to be discovered after reload")
	}
}

func TestUIServer_RestartReportsPerPluginOutcome(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pluginRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[plugins]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	viper.SetConfigFile(configPath)

	writeManifest := func(name, lifecycle string) {
		if err := os.MkdirAll(filepath.Join(pluginRoot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		body := "name=\"" + name + "\"\nenabled=true\nentrypoint=\"" + name + ".sh\"\nlifecycle_mode=\"" + lifecycle + "\"\n"
		if err := os.WriteFile(filepath.Join(pluginRoot, name, "plugin.toml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s manifest: %v", name, err)
		}
	}
	writeManifest("oncall", "on_call")

	mgr := plugins.NewManager(pluginRoot)
	if err := mgr.Discover(); err != nil {
		t.Fatalf("initial discover: %v", err)
	}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 8)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go handleUIClient(ctx, serverConn, mgr, reqOut, plugReg, store, uiReg, nil)

	// Installed after the manager was built: restart must pick it up.
	writeManifest("ondemand", "on_demand_persistent")

	if err := wire.WriteMsg(clientConn, &wire.UIRequest{Type: "restart", ClientID: "cli-test"}); err != nil {
		t.Fatalf("write restart: %v", err)
	}

	scanner := wire.NewScanner(clientConn)
	var resp wire.RestartResponse
	if err := wire.ReadMsg(scanner, &resp); err != nil {
		t.Fatalf("read restart response: %v", err)
	}
	if resp.Type != "restart_response" {
		t.Fatalf("type = %q", resp.Type)
	}
	if !resp.Success {
		t.Fatalf("restart failed: %s", resp.Message)
	}

	byName := make(map[string]wire.RestartResult, len(resp.Results))
	for _, r := range resp.Results {
		byName[r.Name] = r
	}
	if got := byName["oncall"].Status; got != plugins.RestartStatusSkipped {
		t.Fatalf("oncall status = %q, want %q", got, plugins.RestartStatusSkipped)
	}
	if _, ok := byName["ondemand"]; !ok {
		t.Fatalf("restart did not discover the newly installed plugin: %+v", resp.Results)
	}
	if got := byName["ondemand"].Status; got != plugins.RestartStatusStopped {
		t.Fatalf("ondemand status = %q, want %q", got, plugins.RestartStatusStopped)
	}
	if resp.Message != "restarted 0 plugin(s)" {
		t.Fatalf("message = %q, want accurate restarted count", resp.Message)
	}
}

func TestUIServer_RestartTargetsSinglePlugin(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pluginRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[plugins]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	viper.SetConfigFile(configPath)

	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(pluginRoot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		body := "name=\"" + name + "\"\nenabled=true\nentrypoint=\"" + name + ".sh\"\nlifecycle_mode=\"on_call\"\n"
		if err := os.WriteFile(filepath.Join(pluginRoot, name, "plugin.toml"), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s manifest: %v", name, err)
		}
	}

	mgr := plugins.NewManager(pluginRoot)
	if err := mgr.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}

	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 8)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go handleUIClient(ctx, serverConn, mgr, reqOut, plugReg, store, uiReg, nil)

	if err := wire.WriteMsg(clientConn, &wire.UIRequest{Type: "restart", ClientID: "cli-test", Plugin: "beta"}); err != nil {
		t.Fatalf("write restart: %v", err)
	}

	var resp wire.RestartResponse
	if err := wire.ReadMsg(wire.NewScanner(clientConn), &resp); err != nil {
		t.Fatalf("read restart response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Name != "beta" {
		t.Fatalf("expected only beta to be restarted, got %+v", resp.Results)
	}
}

func TestUIServer_RestartUnknownPluginFails(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[plugins]\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	viper.SetConfigFile(configPath)

	mgr := plugins.NewManager(t.TempDir())
	store := newAggregateStore(10, "global", nil, 0.3)
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 8)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go handleUIClient(ctx, serverConn, mgr, reqOut, plugReg, store, uiReg, nil)

	if err := wire.WriteMsg(clientConn, &wire.UIRequest{Type: "restart", ClientID: "cli-test", Plugin: "ghost"}); err != nil {
		t.Fatalf("write restart: %v", err)
	}

	var resp wire.RestartResponse
	if err := wire.ReadMsg(wire.NewScanner(clientConn), &resp); err != nil {
		t.Fatalf("read restart response: %v", err)
	}
	if resp.Success {
		t.Fatal("restarting an unknown plugin should not report success")
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != plugins.RestartStatusError {
		t.Fatalf("expected an error result, got %+v", resp.Results)
	}
}

func TestDispatchQuery_GlobalRespectsGeneralSuggestionEligibility(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	entry := writeScript(t, dir, "oncall.sh", "#!/usr/bin/env bash\nif [[ \"$1\" == \"tarragon\" && \"$2\" == \"query\" ]]; then echo '{\"ok\":true}'; fi\n")

	mgr := plugins.NewManager("-")
	mgr.Plugins["daemon_general"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "daemon_general", Enabled: true, Lifecycle: plugins.LifecycleDaemon, ProvidesGeneral: true}}
	mgr.Plugins["daemon_no_general"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "daemon_no_general", Enabled: true, Lifecycle: plugins.LifecycleDaemon, ProvidesGeneral: false}}
	mgr.Plugins["daemon_require_prefix"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "daemon_require_prefix", Enabled: true, Lifecycle: plugins.LifecycleDaemon, ProvidesGeneral: true, RequirePrefix: true, Prefix: "@d"}}
	mgr.Plugins["ondemand_general"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "ondemand_general", Enabled: true, Lifecycle: plugins.LifecycleOnDemandPersistent, ProvidesGeneral: true}}
	mgr.Plugins["ondemand_no_general"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "ondemand_no_general", Enabled: true, Lifecycle: plugins.LifecycleOnDemandPersistent, ProvidesGeneral: false}}
	mgr.Plugins["oncall_general"] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: "oncall_general", Entrypoint: filepath.Base(entry), Enabled: true, Lifecycle: plugins.LifecycleOnCall, ProvidesGeneral: true}}
	mgr.Plugins["oncall_no_general"] = &plugins.Plugin{Dir: dir, Config: plugins.PluginConfig{Name: "oncall_no_general", Entrypoint: filepath.Base(entry), Enabled: true, Lifecycle: plugins.LifecycleOnCall, ProvidesGeneral: false}}

	store := newAggregateStore(10, "global", nil, 0.3)
	store.create("q-global", "cli", "hello")
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	for _, name := range []string{"daemon_general", "daemon_no_general", "daemon_require_prefix", "ondemand_general", "ondemand_no_general"} {
		srv, cli := net.Pipe()
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cli.Close()
		})
		plugReg.set(name, srv, wire.NewScanner(srv))
	}

	dispatchQuery(ctx, "hello", "q-global", mgr, reqOut, plugReg, store, uiReg, false, "")

	routed := map[string]bool{}
	for {
		select {
		case req := <-reqOut:
			routed[req.name] = true
		default:
			goto done
		}
	}
done:

	if len(routed) != 2 || !routed["daemon_general"] || !routed["ondemand_general"] {
		t.Fatalf("unexpected persistent routing set: %+v", routed)
	}

	store.mu.Lock()
	ag := store.byID["q-global"]
	pluginsState := make(map[string]aggPluginState, len(ag.Plugins))
	for name, state := range ag.Plugins {
		pluginsState[name] = state
	}
	_, hasDaemonGeneral := ag.Plugins["daemon_general"]
	_, hasOndemandGeneral := ag.Plugins["ondemand_general"]
	_, hasOnCallGeneral := ag.Plugins["oncall_general"]
	_, hasDaemonNoGeneral := ag.Plugins["daemon_no_general"]
	_, hasOndemandNoGeneral := ag.Plugins["ondemand_no_general"]
	_, hasOnCallNoGeneral := ag.Plugins["oncall_no_general"]
	_, hasRequirePrefix := ag.Plugins["daemon_require_prefix"]
	store.mu.Unlock()

	if !hasDaemonGeneral || !hasOndemandGeneral || !hasOnCallGeneral {
		t.Fatalf("expected eligible plugins in expected set, got %+v", pluginsState)
	}
	if hasDaemonNoGeneral || hasOndemandNoGeneral || hasOnCallNoGeneral || hasRequirePrefix {
		t.Fatalf("ineligible plugins should not be in expected set, got %+v", pluginsState)
	}
}

func TestDispatchQuery_ExplicitTargetBypassesGeneralEligibility(t *testing.T) {
	ctx := context.Background()
	mgr := plugins.NewManager("-")
	mgr.Plugins["target"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "target", Enabled: true, Lifecycle: plugins.LifecycleDaemon, ProvidesGeneral: false, RequirePrefix: true, Prefix: "@target"}}
	mgr.Plugins["other"] = &plugins.Plugin{Config: plugins.PluginConfig{Name: "other", Enabled: true, Lifecycle: plugins.LifecycleDaemon, ProvidesGeneral: true}}

	store := newAggregateStore(10, "global", nil, 0.3)
	store.create("q-target", "cli", "query")
	uiReg := newUIRegistry()
	reqOut := make(chan pluginRequest, 16)
	plugReg := &pluginRegistry{conns: map[string]net.Conn{}, scanners: map[string]*bufio.Scanner{}}

	for _, name := range []string{"target", "other"} {
		srv, cli := net.Pipe()
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cli.Close()
		})
		plugReg.set(name, srv, wire.NewScanner(srv))
	}

	dispatchQuery(ctx, "query", "q-target", mgr, reqOut, plugReg, store, uiReg, true, "target")

	select {
	case req := <-reqOut:
		if req.name != "target" {
			t.Fatalf("expected target plugin only, got %+v", req)
		}
	default:
		t.Fatalf("expected dispatch to targeted plugin")
	}

	select {
	case req := <-reqOut:
		t.Fatalf("expected only one routed request, got extra: %+v", req)
	default:
	}

	store.mu.Lock()
	_, hasTarget := store.byID["q-target"].Plugins["target"]
	_, hasOther := store.byID["q-target"].Plugins["other"]
	store.mu.Unlock()
	if !hasTarget || hasOther {
		t.Fatalf("unexpected expected-plugin set: target=%v other=%v", hasTarget, hasOther)
	}
}

func TestResolvePrefixTarget(t *testing.T) {
	mgr := &plugins.Manager{Plugins: map[string]*plugins.Plugin{
		"calculator":     {Config: plugins.PluginConfig{ID: "calculator", Enabled: true, Prefix: "calc"}},
		"file_finder":    {Config: plugins.PluginConfig{ID: "file_finder", Enabled: true, Prefix: "f"}},
		"system_control": {Config: plugins.PluginConfig{ID: "system_control", Enabled: true, Prefix: "sys"}},
		"disabled":       {Config: plugins.PluginConfig{ID: "disabled", Enabled: false, Prefix: "d"}},
		"noprefix":       {Config: plugins.PluginConfig{ID: "noprefix", Enabled: true}},
	}}
	mgr.ResolvePrefixes()

	cases := []struct {
		input      string
		wantTarget string
		wantText   string
		wantFound  bool
	}{
		{"@calc 2+2", "calculator", "2+2", true},
		{"@sys reboot", "system_control", "reboot", true},
		{"@f report", "file_finder", "report", true},
		{"  @calc  2+2  ", "calculator", "2+2", true},
		{"@calc", "calculator", "", true},
		{"calc 2+2", "", "", false},
		{"@d hidden", "", "", false},
		{"@unknown thing", "", "", false},
		{"", "", "", false},
	}

	for _, tc := range cases {
		target, text, found := resolvePrefixTarget(tc.input, mgr)
		if found != tc.wantFound || target != tc.wantTarget || text != tc.wantText {
			t.Errorf("resolvePrefixTarget(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.input, target, text, found, tc.wantTarget, tc.wantText, tc.wantFound)
		}
	}
}

func TestResolvePrefixTargetPrefersLongestPrefix(t *testing.T) {
	mgr := &plugins.Manager{Plugins: map[string]*plugins.Plugin{
		"short": {Config: plugins.PluginConfig{ID: "short", Enabled: true, Prefix: "s"}},
		"long":  {Config: plugins.PluginConfig{ID: "long", Enabled: true, Prefix: "sys"}},
	}}
	mgr.ResolvePrefixes()

	target, text, found := resolvePrefixTarget("@sys reboot", mgr)
	if !found || target != "long" || text != "reboot" {
		t.Fatalf("expected longest prefix to win, got (%q, %q, %v)", target, text, found)
	}
}

func TestResolvePrefixTargetBreaksTiesDeterministically(t *testing.T) {
	mgr := &plugins.Manager{Plugins: map[string]*plugins.Plugin{
		"zeta":  {Config: plugins.PluginConfig{ID: "zeta", Enabled: true, Prefix: "x"}},
		"alpha": {Config: plugins.PluginConfig{ID: "alpha", Enabled: true, Prefix: "x"}},
	}}
	mgr.ResolvePrefixes()

	for i := 0; i < 50; i++ {
		target, _, found := resolvePrefixTarget("@x thing", mgr)
		if !found || target != "alpha" {
			t.Fatalf("expected stable tie-break to alpha, got %q (found=%v)", target, found)
		}
	}
}
