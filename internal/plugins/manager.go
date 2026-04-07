package plugins

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/viper"
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
	Name        string        `toml:"name"`
	Description string        `toml:"description"`
	Enabled     bool          `toml:"enabled"`
	Entrypoint  string        `toml:"entrypoint"`
	Lifecycle   LifecycleMode `toml:"lifecycle_mode"`

	ProvidesGeneral bool     `toml:"provides_general_suggestions"`
	Prefix          string   `toml:"prefix"`
	RequirePrefix   bool     `toml:"require_prefix"`
	BuildDeps       []string `toml:"build_dependencies"`
	Capabilities    []string `toml:"capabilities"`
	Icon            string   `toml:"icon"`
}

// Plugin represents a plugin instance loaded from disk.
type Plugin struct {
	Config     PluginConfig
	BaseConfig PluginConfig
	Dir        string

	cmd     *exec.Cmd
	running atomic.Bool
}

// Running reports whether the plugin process is currently running.
func (p *Plugin) Running() bool { return p.running.Load() }

// Stop terminates the plugin process if running.
func (p *Plugin) Stop() { p.stop() }

// Manager oversees all plugins.
type Manager struct {
	Plugins   map[string]*Plugin
	pluginDir string
	mu        sync.RWMutex
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
		Plugins:   make(map[string]*Plugin),
		pluginDir: dir,
	}
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
		cfgPath := filepath.Join(m.pluginDir, ent.Name(), "plugin.toml")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			log.Printf("Skipping plugin %s: %v", ent.Name(), err)
			continue
		}

		cfg := PluginConfig{ProvidesGeneral: true}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			log.Printf("Invalid config for %s: %v", ent.Name(), err)
			continue
		}
		if cfg.Name == "" {
			cfg.Name = ent.Name()
		}

		plugin := &Plugin{Config: cfg, BaseConfig: cfg, Dir: filepath.Join(m.pluginDir, ent.Name())}
		m.Plugins[cfg.Name] = plugin
	}
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
		cfgPath := filepath.Join(m.pluginDir, ent.Name(), "plugin.toml")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			log.Printf("Skipping plugin %s: %v", ent.Name(), err)
			continue
		}

		var cfg PluginConfig
		if err := toml.Unmarshal(data, &cfg); err != nil {
			log.Printf("Invalid config for %s: %v", ent.Name(), err)
			continue
		}
		if cfg.Name == "" {
			cfg.Name = ent.Name()
		}

		if _, exists := m.Plugins[cfg.Name]; exists {
			continue
		}

		plugin := &Plugin{Config: cfg, BaseConfig: cfg, Dir: filepath.Join(m.pluginDir, ent.Name())}
		m.Plugins[cfg.Name] = plugin
	}

	return nil
}

// ApplyOverrides merges [plugins.<name>] config overrides from Viper on top of
// discovered plugin.toml defaults.
//
// Supported override keys:
//   - enabled (bool)
//   - prefix (string)
//   - lifecycle_mode (daemon | on_demand_persistent | on_call)
func (m *Manager) ApplyOverrides() error {
	for name, p := range m.Plugins {
		// Reset to defaults from plugin.toml so removed overrides take effect.
		p.Config = p.BaseConfig

		baseKey := fmt.Sprintf("plugins.%s", name)

		if viper.IsSet(baseKey + ".enabled") {
			p.Config.Enabled = viper.GetBool(baseKey + ".enabled")
		}

		if viper.IsSet(baseKey + ".prefix") {
			p.Config.Prefix = viper.GetString(baseKey + ".prefix")
		}

		if viper.IsSet(baseKey + ".lifecycle_mode") {
			raw := viper.GetString(baseKey + ".lifecycle_mode")
			mode, err := ParseLifecycleMode(raw)
			if err != nil {
				return fmt.Errorf("invalid lifecycle override for plugin %q: %w", name, err)
			}
			p.Config.Lifecycle = mode
		}
	}

	return nil
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
			if err := p.start(ctx, ipcEndpoint); err != nil {
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
	if err := p.start(ctx, ipcEndpoint); err != nil {
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

func (p *Plugin) start(ctx context.Context, ipcEndpoint string) error {
	if p.running.Load() {
		return nil
	}
	if p.Config.Entrypoint == "" {
		return errors.New("missing entrypoint")
	}
	entry := ResolveEntrypoint(p.Dir, p.Config.Entrypoint)
	cmd := exec.CommandContext(ctx, entry)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Provide IPC endpoint and plugin name to the child.
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("TARRAGON_PLUGINS_ENDPOINT=%s", ipcEndpoint),
		fmt.Sprintf("TARRAGON_PLUGIN_NAME=%s", p.Config.Name),
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd = cmd
	p.running.Store(true)

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("plugin %s exited: %v", p.Config.Name, err)
		} else {
			log.Printf("plugin %s exited", p.Config.Name)
		}
		p.running.Store(false)
	}()
	return nil
}

// StopAll stops all running plugin processes.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range m.Plugins {
		p.stop()
	}
}

func (p *Plugin) stop() {
	if !p.running.Load() || p.cmd == nil {
		return
	}
	if err := p.cmd.Process.Kill(); err != nil {
		log.Printf("error killing plugin %s: %v", p.Config.Name, err)
	}
	p.running.Store(false)
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
