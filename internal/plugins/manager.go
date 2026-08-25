package plugins

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/viper"
)

const (
	// DefaultStopTimeout is how long a plugin is given to exit on its own
	// after SIGTERM before it is killed.
	DefaultStopTimeout = 5 * time.Second

	// reapTimeout bounds how long we wait to observe the process exiting
	// after SIGKILL. Reaching it means the process is stuck in the kernel
	// (uninterruptible sleep), and there is nothing further we can do.
	reapTimeout = 2 * time.Second
)

// LifecycleMode represents how a plugin should be executed.
type LifecycleMode string

const (
	LifecycleDaemon             LifecycleMode = "daemon"
	LifecycleOnDemandPersistent LifecycleMode = "on_demand_persistent"
	LifecycleOnCall             LifecycleMode = "on_call"
)

// PluginConfig defines the structure of the plugin configuration file.
type PluginConfig struct {
	// ID is the stable identifier used for config overrides, dispatch
	// routing and IPC. It defaults to the plugin's install directory name.
	ID string `toml:"id"`
	// Name is a human-readable display name and carries no routing meaning.
	Name        string        `toml:"name"`
	Description string        `toml:"description"`
	Source      string        `toml:"source"`
	Enabled     bool          `toml:"enabled"`
	Entrypoint  string        `toml:"entrypoint"`
	Lifecycle   LifecycleMode `toml:"lifecycle_mode"`

	ProvidesGeneral bool     `toml:"provides_general_suggestions"`
	Prefix          string   `toml:"prefix"`
	RequirePrefix   bool     `toml:"require_prefix"`
	BuildDeps       []string `toml:"build_dependencies"`
	Capabilities    []string `toml:"capabilities"`
	Icon            string   `toml:"icon"`

	// ResolvedPrefix is the prefix a user actually types. It is derived from
	// Prefix and the global prefix symbol, and is never read from the manifest.
	ResolvedPrefix string `toml:"-"`
}

// DefaultPrefixSymbol is used when the config does not set prefix_symbol.
const DefaultPrefixSymbol = "@"

// ResolvePrefix combines the configured prefix symbol with a manifest prefix.
//
// A bare token such as "calc" becomes symbol + token, so the symbol stays
// configurable in one place. A token that already begins with a non
// alphanumeric character (for example "@calc" or "=") is treated as literal,
// which keeps manifests written before the symbol existed working unchanged.
func ResolvePrefix(prefix, symbol string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	first := []rune(prefix)[0]
	isAlphanumeric := (first >= 'a' && first <= 'z') ||
		(first >= 'A' && first <= 'Z') ||
		(first >= '0' && first <= '9')
	if !isAlphanumeric {
		return prefix
	}
	if symbol == "" {
		return prefix
	}
	return symbol + prefix
}

// PrefixSymbol returns the configured global prefix symbol.
func PrefixSymbol() string {
	symbol := strings.TrimSpace(viper.GetString("prefix_symbol"))
	if symbol == "" {
		return DefaultPrefixSymbol
	}
	return symbol
}

// Plugin represents a plugin instance loaded from disk.
type Plugin struct {
	Config     PluginConfig
	BaseConfig PluginConfig
	Dir        string

	cmd *exec.Cmd
	// done is closed once the process has been reaped, letting Stop wait for
	// a graceful exit without racing the goroutine that owns cmd.Wait.
	done    chan struct{}
	running atomic.Bool
}

// Running reports whether the plugin process is currently running.
func (p *Plugin) Running() bool { return p.running.Load() }

// Stop asks the plugin process to terminate, escalating to SIGKILL if it does
// not exit within DefaultStopTimeout.
func (p *Plugin) Stop() { p.StopWithTimeout(DefaultStopTimeout) }

// StopWithTimeout sends SIGTERM and waits up to timeout for the process to
// exit before killing it.
func (p *Plugin) StopWithTimeout(timeout time.Duration) {
	h, ok := p.stopHandle()
	if !ok {
		return
	}
	h.stop(timeout)
}

// Manager oversees all plugins.
type Manager struct {
	Plugins     map[string]*Plugin
	pluginDir   string
	stopTimeout time.Duration
	mu          sync.RWMutex
}

// Lock acquires the manager write lock.
func (m *Manager) Lock() { m.mu.Lock() }

// Unlock releases the manager write lock.
func (m *Manager) Unlock() { m.mu.Unlock() }

// RLock acquires the manager read lock.
func (m *Manager) RLock() { m.mu.RLock() }

// RUnlock releases the manager read lock.
func (m *Manager) RUnlock() { m.mu.RUnlock() }

// DefaultDir returns the default plugin directory.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "lib", "tarragon", "plugins")
}

// NewManager creates a new Manager for the given directory.
func NewManager(dir string) *Manager {
	return &Manager{
		Plugins:     make(map[string]*Plugin),
		pluginDir:   dir,
		stopTimeout: DefaultStopTimeout,
	}
}

// SetStopTimeout configures how long plugins are given to shut down cleanly
// before being killed. Non-positive values reset it to DefaultStopTimeout.
func (m *Manager) SetStopTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultStopTimeout
	}
	m.mu.Lock()
	m.stopTimeout = d
	m.mu.Unlock()
}

// StopTimeout returns the configured plugin shutdown grace period.
func (m *Manager) StopTimeout() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.stopTimeout <= 0 {
		return DefaultStopTimeout
	}
	return m.stopTimeout
}

// NormalizePluginID converts raw into a stable identifier that is safe to use
// as a bare TOML key, a Viper path segment and an IPC routing name.
//
// Letters are lowercased, and any run of unsupported characters collapses into
// a single underscore. An empty result means raw carried no usable characters.
func NormalizePluginID(raw string) string {
	var sb strings.Builder
	pendingSep := false
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if pendingSep && sb.Len() > 0 {
				sb.WriteByte('_')
			}
			pendingSep = false
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			if pendingSep && sb.Len() > 0 {
				sb.WriteByte('_')
			}
			pendingSep = false
			sb.WriteRune(r + ('a' - 'A'))
		case r == '_', r == '-':
			pendingSep = sb.Len() > 0
		default:
			pendingSep = sb.Len() > 0
		}
	}
	return sb.String()
}

// loadPluginConfig reads and normalizes a single plugin manifest.
//
// dirName is the install directory name, which is the default identity for
// plugins that do not declare an explicit id.
func loadPluginConfig(pluginDir, dirName string) (PluginConfig, error) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "plugin.toml"))
	if err != nil {
		return PluginConfig{}, err
	}

	cfg := PluginConfig{ProvidesGeneral: true}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return PluginConfig{}, fmt.Errorf("invalid config: %w", err)
	}

	declared := strings.TrimSpace(cfg.ID)
	cfg.ID = NormalizePluginID(declared)
	if declared != "" && cfg.ID != declared {
		log.Printf("Plugin %s: id %q is not a valid identifier, using %q", dirName, declared, cfg.ID)
	}
	if cfg.ID == "" {
		cfg.ID = NormalizePluginID(dirName)
	}
	if cfg.ID == "" {
		return PluginConfig{}, fmt.Errorf("cannot derive a plugin id from directory %q", dirName)
	}

	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}

	return cfg, nil
}

// Discover scans the plugin directory for plugins and loads their configs.
func (m *Manager) Discover() error {
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		return fmt.Errorf("reading plugin dir: %w", err)
	}

	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(m.pluginDir, ent.Name())
		cfg, err := loadPluginConfig(dir, ent.Name())
		if err != nil {
			log.Printf("Skipping plugin %s: %v", ent.Name(), err)
			continue
		}

		m.Plugins[cfg.ID] = &Plugin{Config: cfg, BaseConfig: cfg, Dir: dir}
	}
	m.ResolvePrefixes()
	return nil
}

// DiscoverNew scans the plugin directory and adds only plugins not already
// present in the manager.
//
// Existing plugin entries are left untouched so any in-memory runtime state
// (running process handles, etc.) remains consistent.
func (m *Manager) DiscoverNew() error {
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		return fmt.Errorf("reading plugin dir: %w", err)
	}

	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(m.pluginDir, ent.Name())
		cfg, err := loadPluginConfig(dir, ent.Name())
		if err != nil {
			log.Printf("Skipping plugin %s: %v", ent.Name(), err)
			continue
		}

		if _, exists := m.Plugins[cfg.ID]; exists {
			continue
		}

		m.Plugins[cfg.ID] = &Plugin{Config: cfg, BaseConfig: cfg, Dir: dir}
	}

	m.ResolvePrefixes()
	return nil
}

// RefreshConfigs re-reads manifests for known plugins without replacing their
// runtime process state. New plugins are discovered as well; removed plugins
// remain known until the daemon restarts so a running process can still be
// managed safely.
func (m *Manager) RefreshConfigs() error {
	entries, err := os.ReadDir(m.pluginDir)
	if err != nil {
		return fmt.Errorf("reading plugin dir: %w", err)
	}

	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(m.pluginDir, ent.Name())
		cfg, err := loadPluginConfig(dir, ent.Name())
		if err != nil {
			log.Printf("Skipping plugin %s: %v", ent.Name(), err)
			continue
		}

		if p, ok := m.Plugins[cfg.ID]; ok {
			p.BaseConfig = cfg
			p.Config = cfg
			p.Dir = dir
			continue
		}
		m.Plugins[cfg.ID] = &Plugin{Config: cfg, BaseConfig: cfg, Dir: dir}
	}

	m.ResolvePrefixes()
	return nil
}

// OverrideKeys lists the [plugins.<id>] keys that ApplyOverrides understands.
var OverrideKeys = []string{"enabled", "prefix", "lifecycle_mode"}

// overrideSections returns the config sections consulted for a plugin, in
// precedence order. The stable id wins; the display name is accepted as a
// deprecated fallback so configs written before ids existed keep working.
func overrideSections(id string, cfg PluginConfig) []string {
	sections := []string{id}
	if name := strings.TrimSpace(cfg.Name); name != "" && !strings.EqualFold(name, id) {
		sections = append(sections, name)
	}
	return sections
}

// lookupOverride finds the first section that sets key, returning its full
// Viper path.
func lookupOverride(sections []string, key string) (string, bool) {
	for _, section := range sections {
		path := fmt.Sprintf("plugins.%s.%s", section, key)
		if viper.IsSet(path) {
			return path, true
		}
	}
	return "", false
}

// ApplyOverrides merges [plugins.<id>] config overrides from Viper on top of
// discovered plugin.toml defaults.
//
// Supported override keys:
//   - enabled (bool)
//   - prefix (string)
//   - lifecycle_mode (daemon | on_demand_persistent | on_call)
func (m *Manager) ApplyOverrides() error {
	for _, warning := range m.ValidateOverrideKeys() {
		log.Printf("config: %s", warning)
	}

	for id, p := range m.Plugins {
		// Reset to defaults from plugin.toml so removed overrides take effect.
		p.Config = p.BaseConfig

		sections := overrideSections(id, p.BaseConfig)

		if path, ok := lookupOverride(sections, "enabled"); ok {
			p.Config.Enabled = viper.GetBool(path)
		}

		if path, ok := lookupOverride(sections, "prefix"); ok {
			p.Config.Prefix = viper.GetString(path)
		}

		if path, ok := lookupOverride(sections, "lifecycle_mode"); ok {
			mode, err := ParseLifecycleMode(viper.GetString(path))
			if err != nil {
				return fmt.Errorf("invalid lifecycle override for plugin %q: %w", id, err)
			}
			p.Config.Lifecycle = mode
		}
	}

	m.ResolvePrefixes()
	for _, collision := range m.PrefixCollisions() {
		log.Printf("config: %s", collision)
	}

	return nil
}

// ResolvePrefixes recomputes every plugin's effective prefix from its manifest
// prefix and the configured prefix symbol.
func (m *Manager) ResolvePrefixes() {
	symbol := PrefixSymbol()
	for _, p := range m.Plugins {
		p.Config.ResolvedPrefix = ResolvePrefix(p.Config.Prefix, symbol)
	}
}

// PrefixCollisions reports effective prefixes claimed by more than one enabled
// plugin. Dispatch still resolves deterministically by id, but only one of the
// plugins can ever be reached by that prefix.
func (m *Manager) PrefixCollisions() []string {
	owners := make(map[string][]string)
	for id, p := range m.Plugins {
		if p.Config.Enabled && p.Config.ResolvedPrefix != "" {
			owners[p.Config.ResolvedPrefix] = append(owners[p.Config.ResolvedPrefix], id)
		}
	}

	collisions := make([]string, 0, len(owners))
	for prefix, ids := range owners {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		collisions = append(collisions, fmt.Sprintf(
			"prefix %q is claimed by %s; only %s is reachable",
			prefix, strings.Join(ids, ", "), ids[0]))
	}
	sort.Strings(collisions)
	return collisions
}

// ValidateOverrideKeys reports [plugins.*] config entries that will not take
// effect, such as sections naming an unknown plugin, sections still keyed by a
// display name, and unrecognized keys within a valid section.
//
// Viper lowercases config keys, so all comparisons here are case-insensitive.
func (m *Manager) ValidateOverrideKeys() []string {
	raw := viper.GetStringMap("plugins")
	if len(raw) == 0 {
		return nil
	}

	byID := make(map[string]string, len(m.Plugins))
	byName := make(map[string]string, len(m.Plugins))
	for id, p := range m.Plugins {
		byID[strings.ToLower(id)] = id
		if name := strings.TrimSpace(p.BaseConfig.Name); name != "" && !strings.EqualFold(name, id) {
			byName[strings.ToLower(name)] = id
		}
	}

	known := make(map[string]bool, len(OverrideKeys))
	for _, key := range OverrideKeys {
		known[key] = true
	}

	sections := make([]string, 0, len(raw))
	for section := range raw {
		sections = append(sections, section)
	}
	sort.Strings(sections)

	var warnings []string
	for _, section := range sections {
		id, ok := byID[section]
		if !ok {
			if target, deprecated := byName[section]; deprecated {
				warnings = append(warnings, fmt.Sprintf(
					"[plugins.%q] is keyed by display name; rename the section to [plugins.%s]", section, target))
				id = target
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"[plugins.%s] does not match any installed plugin id and is ignored", section))
				continue
			}
		}

		values, isTable := raw[section].(map[string]any)
		if !isTable {
			warnings = append(warnings, fmt.Sprintf("[plugins.%s] is not a table and is ignored", section))
			continue
		}

		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !known[key] {
				warnings = append(warnings, fmt.Sprintf(
					"[plugins.%s] has unknown key %q and is ignored (supported: %s)",
					id, key, strings.Join(OverrideKeys, ", ")))
			}
		}
	}

	return warnings
}

// ParseLifecycleMode validates and converts lifecycle mode string.
func ParseLifecycleMode(raw string) (LifecycleMode, error) {
	mode := LifecycleMode(raw)
	switch mode {
	case LifecycleDaemon, LifecycleOnDemandPersistent, LifecycleOnCall:
		return mode, nil
	default:
		return "", fmt.Errorf("must be one of %q, %q, %q", LifecycleDaemon, LifecycleOnDemandPersistent, LifecycleOnCall)
	}
}

// StartPersistent starts all daemon lifecycle plugins.
func (m *Manager) StartPersistent(ctx context.Context, ipcEndpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, p := range m.Plugins {
		if !p.Config.Enabled {
			continue
		}
		if p.Config.Lifecycle == LifecycleDaemon {
			log.Printf("starting plugin %s (lifecycle=%s)", name, p.Config.Lifecycle)
			if err := p.start(ctx, ipcEndpoint, m.stopTimeout); err != nil {
				log.Printf("failed to start plugin %s: %v", name, err)
				return fmt.Errorf("start plugin %s: %w", name, err)
			}
			log.Printf("started plugin %s (pid=%d)", name, p.cmd.Process.Pid)
		}
	}
	return nil
}

// StartOnDemand starts a single on-demand persistent plugin if needed.
func (m *Manager) StartOnDemand(ctx context.Context, name string, ipcEndpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.Plugins[name]
	if !ok {
		return nil
	}
	if !p.Config.Enabled || p.running.Load() {
		return nil
	}
	if p.Config.Lifecycle != LifecycleOnDemandPersistent {
		return nil
	}

	log.Printf("starting plugin %s (lifecycle=%s)", name, p.Config.Lifecycle)
	if err := p.start(ctx, ipcEndpoint, m.stopTimeout); err != nil {
		return err
	}
	if p.cmd != nil && p.cmd.Process != nil {
		log.Printf("started plugin %s (pid=%d)", name, p.cmd.Process.Pid)
	}

	return nil
}

// IsRunning reports whether the named plugin is running.
func (m *Manager) IsRunning(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.Plugins[name]
	if !ok {
		return false
	}
	return p.running.Load()
}

func (p *Plugin) start(ctx context.Context, ipcEndpoint string, stopTimeout time.Duration) error {
	if p.running.Load() {
		return nil
	}
	if p.Config.Entrypoint == "" {
		return errors.New("missing entrypoint")
	}
	if stopTimeout <= 0 {
		stopTimeout = DefaultStopTimeout
	}

	entry := ResolveEntrypoint(p.Dir, p.Config.Entrypoint)
	cmd := exec.CommandContext(ctx, entry)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// CommandContext kills the child outright when ctx is cancelled. Override
	// that so context cancellation follows the same graceful path as an
	// explicit Stop: SIGTERM first, and let WaitDelay escalate to SIGKILL only
	// if the plugin ignores it.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = stopTimeout
	// Provide IPC endpoint and plugin identity to the child. TARRAGON_PLUGIN_NAME
	// carries the routing id, since that is the value the daemon matches against
	// the plugin's hello message.
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("TARRAGON_PLUGINS_ENDPOINT=%s", ipcEndpoint),
		fmt.Sprintf("TARRAGON_PLUGIN_NAME=%s", p.Config.ID),
		fmt.Sprintf("TARRAGON_PLUGIN_ID=%s", p.Config.ID),
		fmt.Sprintf("TARRAGON_PLUGIN_DISPLAY_NAME=%s", p.Config.Name),
		fmt.Sprintf("TARRAGON_PREFIX_SYMBOL=%s", PrefixSymbol()),
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	p.cmd = cmd
	p.done = done
	p.running.Store(true)

	name := p.Config.ID
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("plugin %s exited: %v", name, err)
		} else {
			log.Printf("plugin %s exited", name)
		}
		p.running.Store(false)
		close(done)
	}()
	return nil
}

// stopHandle is a snapshot of everything needed to terminate a plugin
// process. Taking it under the manager lock and stopping outside of it keeps
// the lock free while we wait for plugins to shut down, and pins the exact
// process we intend to signal even if the plugin is restarted concurrently.
type stopHandle struct {
	plugin *Plugin
	name   string
	proc   *os.Process
	done   chan struct{}
}

func (p *Plugin) stopHandle() (stopHandle, bool) {
	if !p.running.Load() || p.cmd == nil || p.cmd.Process == nil {
		return stopHandle{}, false
	}
	return stopHandle{plugin: p, name: p.Config.ID, proc: p.cmd.Process, done: p.done}, true
}

// stop terminates the process, preferring a clean exit.
func (h stopHandle) stop(timeout time.Duration) {
	if timeout <= 0 {
		timeout = DefaultStopTimeout
	}
	defer h.plugin.running.Store(false)

	switch err := h.proc.Signal(syscall.SIGTERM); {
	case err == nil:
		if h.waitFor(timeout) {
			log.Printf("plugin %s stopped gracefully", h.name)
			return
		}
		log.Printf("plugin %s did not exit within %s; sending SIGKILL", h.name, timeout)
	case errors.Is(err, os.ErrProcessDone):
		return
	default:
		log.Printf("plugin %s: SIGTERM failed (%v); sending SIGKILL", h.name, err)
	}

	if err := h.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.Printf("error killing plugin %s: %v", h.name, err)
	}
	if !h.waitFor(reapTimeout) {
		log.Printf("plugin %s did not terminate after SIGKILL", h.name)
	}
}

// waitFor reports whether the process exited within d.
func (h stopHandle) waitFor(d time.Duration) bool {
	if h.done == nil {
		return false
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-h.done:
		return true
	case <-timer.C:
		return false
	}
}

// stopHandles terminates the given processes concurrently, so overall
// shutdown time is bounded by the slowest plugin rather than their sum.
func stopHandles(handles []stopHandle, timeout time.Duration) {
	var wg sync.WaitGroup
	for _, h := range handles {
		wg.Add(1)
		go func(h stopHandle) {
			defer wg.Done()
			h.stop(timeout)
		}(h)
	}
	wg.Wait()
}

// StopAll stops all running plugin processes.
func (m *Manager) StopAll() {
	m.mu.Lock()
	timeout := m.stopTimeout
	handles := make([]stopHandle, 0, len(m.Plugins))
	for _, p := range m.Plugins {
		if h, ok := p.stopHandle(); ok {
			handles = append(handles, h)
		}
	}
	m.mu.Unlock()

	stopHandles(handles, timeout)
}

// StopPlugins stops the named running plugins concurrently. Unknown or
// already-stopped names are ignored.
func (m *Manager) StopPlugins(names []string) {
	m.mu.Lock()
	timeout := m.stopTimeout
	handles := make([]stopHandle, 0, len(names))
	for _, name := range names {
		p, ok := m.Plugins[name]
		if !ok {
			continue
		}
		if h, ok := p.stopHandle(); ok {
			handles = append(handles, h)
		}
	}
	m.mu.Unlock()

	stopHandles(handles, timeout)
}

// Restart status values reported by Manager.Restart.
const (
	RestartStatusRestarted = "restarted"
	RestartStatusStopped   = "stopped"
	RestartStatusSkipped   = "skipped"
	RestartStatusError     = "error"
)

// RestartResult reports the outcome of restarting a single plugin.
type RestartResult struct {
	Name    string
	Status  string
	Message string
}

// Restart bounces plugin processes.
//
// Passing no names targets every known plugin. Behaviour depends on lifecycle:
//   - daemon: stopped and started again
//   - on_demand_persistent: stopped only, since it is started by demand
//   - on_call: nothing to do, it has no long-lived process
//
// Disabled plugins are stopped but not started.
func (m *Manager) Restart(ctx context.Context, ipcEndpoint string, names []string) []RestartResult {
	m.mu.Lock()
	timeout := m.stopTimeout

	explicit := len(names) > 0
	if !explicit {
		names = make([]string, 0, len(m.Plugins))
		for name := range m.Plugins {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	results := make([]RestartResult, 0, len(names))
	handles := make([]stopHandle, 0, len(names))
	var toStart []string

	for _, name := range names {
		p, ok := m.Plugins[name]
		if !ok {
			results = append(results, RestartResult{
				Name:    name,
				Status:  RestartStatusError,
				Message: "plugin not found",
			})
			continue
		}

		if h, running := p.stopHandle(); running {
			handles = append(handles, h)
		}

		switch {
		case !p.Config.Enabled:
			results = append(results, RestartResult{
				Name:    name,
				Status:  RestartStatusStopped,
				Message: "plugin is disabled",
			})
		case p.Config.Lifecycle == LifecycleOnCall:
			results = append(results, RestartResult{
				Name:    name,
				Status:  RestartStatusSkipped,
				Message: "on_call plugins have no persistent process",
			})
		case p.Config.Lifecycle == LifecycleOnDemandPersistent:
			results = append(results, RestartResult{
				Name:    name,
				Status:  RestartStatusStopped,
				Message: "will start again on the next matching query",
			})
		default:
			toStart = append(toStart, name)
			results = append(results, RestartResult{Name: name, Status: RestartStatusRestarted})
		}
	}
	m.mu.Unlock()

	// Stop outside the lock: waiting for plugins to exit while holding it
	// would stall every concurrent query for the whole grace period.
	stopHandles(handles, timeout)

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range toStart {
		p, ok := m.Plugins[name]
		if !ok {
			continue
		}
		if err := p.start(ctx, ipcEndpoint, m.stopTimeout); err != nil {
			log.Printf("failed to restart plugin %s: %v", name, err)
			setRestartResult(results, name, RestartStatusError, err.Error())
			continue
		}
		log.Printf("restarted plugin %s (pid=%d)", name, p.cmd.Process.Pid)
	}

	return results
}

func setRestartResult(results []RestartResult, name, status, message string) {
	for i := range results {
		if results[i].Name == name {
			results[i].Status = status
			results[i].Message = message
			return
		}
	}
}

// ResolveEntrypoint returns an absolute executable path for a plugin entrypoint.
//
// Relative entrypoints are resolved against the plugin directory; absolute
// entrypoints are returned as-is.
func ResolveEntrypoint(pluginDir, entrypoint string) string {
	if filepath.IsAbs(entrypoint) {
		return entrypoint
	}
	return filepath.Join(pluginDir, entrypoint)
}
