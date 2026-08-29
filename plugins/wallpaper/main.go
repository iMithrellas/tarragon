package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultPluginName = "wallpaper"
	randomID          = "wallpaper:random"
	previousID        = "wallpaper:previous"
)

// ─── Tarragon wire types ─────────────────────────────────────────────────

type pluginMessage struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	QueryID  string `json:"query_id,omitempty"`
	Text     string `json:"text,omitempty"`
	ResultID string `json:"result_id,omitempty"`
	Action   string `json:"action,omitempty"`
	Data     any    `json:"data,omitempty"`
}

type actionItem struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

type resultItem struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description,omitempty"`
	Icon        string       `json:"icon,omitempty"`
	Category    string       `json:"category,omitempty"`
	PreviewPath string       `json:"preview_path,omitempty"`
	Score       float64      `json:"score"`
	Actions     []actionItem `json:"actions,omitempty"`
}

type responseData struct {
	Results []resultItem `json:"results"`
	Error   string       `json:"error,omitempty"`
}

type selectResponse struct {
	Type    string `json:"type"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// ─── Logging ─────────────────────────────────────────────────────────────

// Logger writes to stderr, which the daemon forwards to the journal.
type Logger struct{ name string }

func (l *Logger) Info(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[PLUGIN: %s] INFO %s\n", l.name, fmt.Sprintf(format, args...))
}

func (l *Logger) Error(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[PLUGIN: %s] ERROR %s\n", l.name, fmt.Sprintf(format, args...))
}

// ─── Plugin ──────────────────────────────────────────────────────────────

type plugin struct {
	name    string
	cfg     *Config
	lib     *Library
	state   *State
	applier *Applier
	log     *Logger
}

func newPlugin(name string) (*plugin, error) {
	log := &Logger{name: name}

	cfg, cfgPath, err := loadConfig()
	if err != nil {
		// Fall back to defaults rather than refusing to start.
		log.Error("%v (continuing with defaults)", err)
	} else {
		log.Info("config: %s", cfgPath)
	}

	state := loadState()
	return &plugin{
		name:    name,
		cfg:     cfg,
		lib:     NewLibrary(cfg.Directories),
		state:   state,
		applier: NewApplier(cfg, state, log),
		log:     log,
	}, nil
}

func main() {
	var (
		setPath  string
		restore  bool
		list     bool
		once     string
		showInfo bool
	)
	flag.StringVar(&setPath, "set", "", "set the given wallpaper and exit")
	flag.BoolVar(&restore, "restore", false, "re-apply the persisted wallpaper and exit")
	flag.BoolVar(&list, "list", false, "print the wallpaper library and exit")
	flag.StringVar(&once, "once", "", "run a single query and print JSON, then exit")
	flag.BoolVar(&showInfo, "info", false, "print backend/config/state diagnostics and exit")

	// The Tarragon on-call CLI contract uses bare subcommands
	// (`wallpaper tarragon query ...`), which flag.Parse would choke on.
	if len(os.Args) > 1 && os.Args[1] == "tarragon" {
		os.Exit(runCLIContract(os.Args[2:]))
	}
	flag.Parse()

	name := os.Getenv("TARRAGON_PLUGIN_NAME")
	if name == "" {
		name = defaultPluginName
	}

	p, err := newPlugin(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()

	switch {
	case showInfo:
		os.Exit(p.printInfo(ctx))
	case restore:
		if err := p.applier.Restore(ctx); err != nil {
			p.log.Error("%v", err)
			os.Exit(1)
		}
		return
	case setPath != "":
		abs, err := filepath.Abs(expandPath(setPath))
		if err != nil {
			p.log.Error("%v", err)
			os.Exit(1)
		}
		if err := p.applier.Apply(ctx, abs); err != nil {
			p.log.Error("%v", err)
			os.Exit(1)
		}
		p.log.Info("wallpaper set to %s", abs)
		return
	case list:
		_ = p.lib.Scan(ctx, p.cfg)
		for _, e := range p.lib.All() {
			fmt.Println(e.Path)
		}
		return
	case once != "":
		_ = p.lib.Scan(ctx, p.cfg)
		out := pluginMessage{Type: "response", Data: p.buildResults(once)}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	// Index synchronously before announcing ourselves: the daemon can send a
	// query the moment it sees our hello, and a query that races the first
	// scan would wrongly report an empty library. A wallpaper library is
	// small enough that this stat-only walk costs milliseconds.
	p.initialScan(ctx)
	go p.rescanLoop(ctx)

	if p.cfg.RestoreOnStart {
		go func() {
			if err := p.applier.Restore(ctx); err != nil && ctx.Err() == nil {
				p.log.Error("restore failed: %v", err)
			}
		}()
	}

	endpoint := os.Getenv("TARRAGON_PLUGINS_ENDPOINT")
	if endpoint == "" {
		p.log.Info("no TARRAGON_PLUGINS_ENDPOINT set; idling (wallpaper restore still applied)")
		<-ctx.Done()
		return
	}

	if err := p.serve(ctx, endpoint); err != nil && !errors.Is(err, context.Canceled) {
		p.log.Error("plugin loop failed: %v", err)
		os.Exit(1)
	}
}

// ─── Indexing ────────────────────────────────────────────────────────────

func (p *plugin) initialScan(ctx context.Context) {
	start := time.Now()
	if err := p.lib.Scan(ctx, p.cfg); err != nil && ctx.Err() == nil {
		p.log.Error("initial library scan failed: %v", err)
	}
	p.log.Info("indexed %d wallpapers from %d directories in %s",
		p.lib.Len(), len(p.cfg.Directories), time.Since(start).Round(time.Millisecond))
	if p.lib.Len() == 0 {
		p.log.Info("library empty; set `directories` in %s", configPath())
	}
}

func (p *plugin) rescanLoop(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.rescanInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.lib.Scan(ctx, p.cfg); err != nil && ctx.Err() == nil {
				p.log.Error("library rescan failed: %v", err)
			}
		}
	}
}

// ─── Query ───────────────────────────────────────────────────────────────

func (p *plugin) buildResults(query string) responseData {
	query = strings.TrimSpace(query)
	current := p.state.Current()

	results := make([]resultItem, 0, p.cfg.MaxResults+2)
	results = append(results, p.specialResults(query, current)...)

	for _, m := range p.lib.Search(query, p.cfg.MaxResults) {
		e := m.Entry
		desc := filepath.Dir(e.Rel)
		if desc == "." {
			desc = e.Root
		}
		label := prettify(e.Name)
		if e.Path == current {
			label += "  (current)"
		}
		results = append(results, resultItem{
			ID:          e.Path,
			Label:       label,
			Description: fmt.Sprintf("%s · %s", desc, humanSize(e.Size)),
			Icon:        "image-x-generic",
			Category:    "Wallpapers",
			PreviewPath: e.Path,
			Score:       m.Score,
			Actions:     wallpaperActions(),
		})
	}

	if len(results) == 0 {
		if p.lib.Len() == 0 {
			return responseData{
				Results: []resultItem{},
				Error:   fmt.Sprintf("no wallpapers found; configure `directories` in %s", configPath()),
			}
		}
		return responseData{Results: []resultItem{}}
	}
	return responseData{Results: results}
}

func wallpaperActions() []actionItem {
	return []actionItem{
		{Name: "set", Default: true, Description: "Set wallpaper"},
	}
}

// specialResults injects the synthetic "random"/"previous" entries when the
// query is empty or plausibly targeting them.
func (p *plugin) specialResults(query, current string) []resultItem {
	if p.lib.Len() == 0 {
		return nil
	}
	q := strings.ToLower(query)
	var out []resultItem

	if q == "" || strings.HasPrefix("random", q) || strings.HasPrefix("shuffle", q) {
		out = append(out, resultItem{
			ID:          randomID,
			Label:       "Random wallpaper",
			Description: fmt.Sprintf("Pick one of %d wallpapers at random", p.lib.Len()),
			Icon:        "media-playlist-shuffle",
			Category:    "Wallpapers",
			Score:       1.5,
			Actions:     wallpaperActions(),
		})
	}

	if prev := p.previousWallpaper(); prev != "" && (q == "" || strings.HasPrefix("previous", q)) {
		out = append(out, resultItem{
			ID:          previousID,
			Label:       "Previous wallpaper",
			Description: prettify(strings.TrimSuffix(filepath.Base(prev), filepath.Ext(prev))),
			Icon:        "edit-undo",
			Category:    "Wallpapers",
			PreviewPath: prev,
			Score:       1.4,
			Actions:     wallpaperActions(),
		})
	}
	return out
}

func (p *plugin) previousWallpaper() string {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if len(p.state.History) < 2 {
		return ""
	}
	return p.state.History[1]
}

// ─── Selection ───────────────────────────────────────────────────────────

// resolveSelection maps a result id (possibly synthetic) to a real path.
func (p *plugin) resolveSelection(id string) (string, error) {
	switch id {
	case randomID:
		e, ok := p.lib.Random(p.state.Current())
		if !ok {
			return "", fmt.Errorf("wallpaper library is empty")
		}
		return e.Path, nil
	case previousID:
		prev := p.previousWallpaper()
		if prev == "" {
			return "", fmt.Errorf("no previous wallpaper recorded")
		}
		return prev, nil
	case "":
		return "", fmt.Errorf("empty result id")
	default:
		return id, nil
	}
}

func (p *plugin) handleSelect(ctx context.Context, id, action string) selectResponse {
	path, err := p.resolveSelection(id)
	if err != nil {
		return selectResponse{Type: "select_response", Success: false, Message: err.Error()}
	}

	switch action {
	case "", "set", "open":
	default:
		return selectResponse{
			Type:    "select_response",
			Success: false,
			Message: fmt.Sprintf("unknown action %q; use set", action),
		}
	}

	if err := p.applier.Apply(ctx, path); err != nil {
		return selectResponse{Type: "select_response", Success: false, Message: err.Error()}
	}

	label := prettify(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	msg := "Wallpaper set to " + label
	if p.applier.BackendName() == "matugen" {
		msg += " (matugen applied)"
	}
	return selectResponse{Type: "select_response", Success: true, Message: msg}
}

// ─── Socket loop ─────────────────────────────────────────────────────────

func (p *plugin) serve(ctx context.Context, endpoint string) error {
	conn, err := connectWithRetry(ctx, endpoint, 20, 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = conn.Write(append(b, '\n'))
		return err
	}

	if err := write(pluginMessage{Type: "hello", Name: p.name}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	p.log.Info("connected to daemon at %s", endpoint)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		var msg pluginMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			p.log.Error("invalid message: %v", err)
			continue
		}

		switch msg.Type {
		case "request":
			if err := write(pluginMessage{
				Type:    "response",
				QueryID: msg.QueryID,
				Data:    p.buildResults(msg.Text),
			}); err != nil {
				return fmt.Errorf("write response: %w", err)
			}

		case "select":
			// Applying can take seconds (transition + matugen), so it must
			// not block the read loop or later queries would queue behind it.
			go func(m pluginMessage) {
				p.log.Info("select result_id=%s action=%q", m.ResultID, m.Action)
				resp := p.handleSelect(ctx, m.ResultID, m.Action)
				if !resp.Success {
					p.log.Error("select failed: %s", resp.Message)
				}
				if err := write(resp); err != nil {
					p.log.Error("write select_response: %v", err)
				}
			}(msg)

		default:
			p.log.Info("ignoring message type=%q", msg.Type)
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		return err
	}
	return ctx.Err()
}

func connectWithRetry(ctx context.Context, endpoint string, attempts int, delay time.Duration) (net.Conn, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		conn, err := net.Dial("unix", endpoint)
		if err == nil {
			return conn, nil
		}
		lastErr = err

		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	return nil, fmt.Errorf("connect to %s failed after %d attempts: %w", endpoint, attempts, lastErr)
}

// ─── On-call CLI contract ────────────────────────────────────────────────

// runCLIContract implements `wallpaper tarragon <manifest|query|select>` so
// the plugin also works when installed via `tarragon plugin enable` or run
// with lifecycle_mode = "on_call".
func runCLIContract(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: wallpaper tarragon <manifest|query|select>")
		return 2
	}

	if args[0] == "manifest" {
		fmt.Print(manifestTOML)
		return 0
	}

	name := os.Getenv("TARRAGON_PLUGIN_NAME")
	if name == "" {
		name = defaultPluginName
	}
	p, err := newPlugin(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch args[0] {
	case "query":
		query := ""
		if len(args) > 1 {
			query = strings.Join(args[1:], " ")
		}
		_ = p.lib.Scan(ctx, p.cfg)
		if err := json.NewEncoder(os.Stdout).Encode(p.buildResults(query)); err != nil {
			return 1
		}
		return 0

	case "select":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: wallpaper tarragon select <result-id> [action]")
			return 2
		}
		action := ""
		if len(args) > 2 {
			action = args[2]
		}
		_ = p.lib.Scan(ctx, p.cfg)
		resp := p.handleSelect(ctx, args[1], action)
		_ = json.NewEncoder(os.Stdout).Encode(resp)
		if !resp.Success {
			return 1
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[0])
		return 2
	}
}

// manifestTOML is the manifest served by `wallpaper tarragon manifest`. It is
// embedded from plugin.toml so the system-enable path can never drift from the
// manifest shipped with the plugin.
//
//go:embed plugin.toml
var manifestTOML string

// ─── Diagnostics ─────────────────────────────────────────────────────────

func (p *plugin) printInfo(ctx context.Context) int {
	_ = p.lib.Scan(ctx, p.cfg)

	fmt.Printf("config file : %s\n", configPath())
	fmt.Printf("state file  : %s\n", statePath())
	fmt.Printf("directories :\n")
	for _, d := range p.cfg.Directories {
		mark := "missing"
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			mark = "ok"
		}
		fmt.Printf("  - %-50s [%s]\n", d, mark)
	}
	fmt.Printf("wallpapers  : %d\n", p.lib.Len())

	fmt.Printf("backend     : %s (configured: %s)\n", p.applier.BackendName(), p.cfg.Backend)
	fmt.Printf("available   :")
	for _, n := range backendOrder {
		if b, err := newBackend(n); err == nil && b.Available() {
			fmt.Printf(" %s", n)
		}
	}
	fmt.Println()

	fmt.Printf("matugen     : mode=%s type=%s prefer=%s\n", p.cfg.MatugenMode, p.cfg.MatugenType, p.cfg.MatugenPrefer)
	if cur := p.state.Current(); cur != "" {
		fmt.Printf("current     : %s (set %s via %s)\n", cur,
			p.state.AppliedAt.Format(time.RFC3339), p.state.Backend)
	} else {
		fmt.Printf("current     : <none>\n")
	}
	return 0
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
