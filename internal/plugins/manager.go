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
	Config PluginConfig
	Dir    string

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

		var cfg PluginConfig
		if err := toml.Unmarshal(data, &cfg); err != nil {
			log.Printf("Invalid config for %s: %v", ent.Name(), err)
			continue
		}
		if cfg.Name == "" {
			cfg.Name = ent.Name()
		}

		plugin := &Plugin{Config: cfg, Dir: filepath.Join(m.pluginDir, ent.Name())}
		m.Plugins[cfg.Name] = plugin
	}
	return nil
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

// ApplyOverrides merges config overrides from Viper over discovered plugin config.
// Supported keys are:
//   - plugins.<name>.enabled
//   - plugins.<name>.prefix
//   - plugins.<name>.lifecycle_mode
//
// Only fields explicitly set in Viper are overridden.
func (m *Manager) ApplyOverrides() {
	for name, p := range m.Plugins {
		enabledKey := fmt.Sprintf("plugins.%s.enabled", name)
		if viper.IsSet(enabledKey) {
			p.Config.Enabled = viper.GetBool(enabledKey)
		}

		prefixKey := fmt.Sprintf("plugins.%s.prefix", name)
		if viper.IsSet(prefixKey) {
			p.Config.Prefix = viper.GetString(prefixKey)
		}

		lifecycleKey := fmt.Sprintf("plugins.%s.lifecycle_mode", name)
		if viper.IsSet(lifecycleKey) {
			lifecycle := LifecycleMode(viper.GetString(lifecycleKey))
			switch lifecycle {
			case LifecycleDaemon, LifecycleOnDemandPersistent, LifecycleOnCall:
				p.Config.Lifecycle = lifecycle
			default:
				log.Printf("invalid lifecycle_mode override for plugin %s: %q", name, lifecycle)
			}
		}
	}
}

func (p *Plugin) start(ctx context.Context, ipcEndpoint string) error {
	if p.running.Load() {
		return nil
	}
	if p.Config.Entrypoint == "" {
		return errors.New("missing entrypoint")
	}
	entry := filepath.Join(p.Dir, p.Config.Entrypoint)
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
