package plugins

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
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
	BuildDeps       []string `toml:"build_dependencies"`
	Capabilities    []string `toml:"capabilities"`
	Icon            string   `toml:"icon"`
}

// Plugin represents a plugin instance loaded from disk.
type Plugin struct {
	Config PluginConfig
	Dir    string

	cmd     *exec.Cmd
	running bool
}

// Manager oversees all plugins.
type Manager struct {
	Plugins   map[string]*Plugin
	pluginDir string
}

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

func (p *Plugin) start(ctx context.Context, ipcEndpoint string) error {
	if p.running {
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
	p.running = true

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("plugin %s exited: %v", p.Config.Name, err)
		} else {
			log.Printf("plugin %s exited", p.Config.Name)
		}
		p.running = false
	}()
	return nil
}

// StopAll stops all running plugin processes.
func (m *Manager) StopAll() {
	for _, p := range m.Plugins {
		p.stop()
	}
}

func (p *Plugin) stop() {
	if !p.running || p.cmd == nil {
		return
	}
	if err := p.cmd.Process.Kill(); err != nil {
		log.Printf("error killing plugin %s: %v", p.Config.Name, err)
	}
	p.running = false
}
