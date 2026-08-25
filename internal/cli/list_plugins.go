package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mithrel-dots/tarragon/internal/plugins"
	"github.com/mithrel-dots/tarragon/internal/texttable"
	"github.com/mithrel-dots/tarragon/internal/wire"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

type installedPlugin struct {
	ID            string `toml:"id"`
	Name          string `toml:"name"`
	Description   string `toml:"description"`
	LifecycleMode string `toml:"lifecycle_mode"`
	Enabled       bool   `toml:"enabled"`
}

var listPluginsCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed plugins",
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginRoot := plugins.DefaultDir()
		if pluginRoot == "" {
			return errors.New("could not determine plugin installation directory")
		}

		entries, err := os.ReadDir(pluginRoot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No plugins installed."); err != nil {
					return err
				}
				return nil
			}
			return fmt.Errorf("read plugin directory: %w", err)
		}

		loaded, daemonAvailable := loadedPluginsFromDaemon(750 * time.Millisecond)
		installed := make([]installedPlugin, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			pluginTomlPath := filepath.Join(pluginRoot, entry.Name(), "plugin.toml")
			data, err := os.ReadFile(pluginTomlPath)
			if err != nil {
				continue
			}

			var cfg installedPlugin
			if err := toml.Unmarshal(data, &cfg); err != nil {
				continue
			}

			cfg.ID = plugins.NormalizePluginID(cfg.ID)
			if cfg.ID == "" {
				cfg.ID = plugins.NormalizePluginID(entry.Name())
			}
			if cfg.ID == "" {
				continue
			}
			if cfg.Name == "" {
				cfg.Name = cfg.ID
			}

			installed = append(installed, cfg)
		}

		if len(installed) == 0 {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No plugins installed."); err != nil {
				return err
			}
			return nil
		}

		sort.Slice(installed, func(i, j int) bool { return installed[i].ID < installed[j].ID })
		rows := make([][]string, 0, len(installed))
		for _, cfg := range installed {
			loadedValue := "unknown"
			if daemonAvailable {
				_, ok := loaded[cfg.ID]
				loadedValue = fmt.Sprintf("%t", ok)
			}
			rows = append(rows, []string{
				cfg.ID,
				cfg.Name,
				cfg.Description,
				cfg.LifecycleMode,
				fmt.Sprintf("%t", cfg.Enabled),
				loadedValue,
			})
		}

		texttable.Render(cmd.OutOrStdout(), []texttable.Column{
			{Header: "ID"},
			{Header: "NAME"},
			{Header: "DESCRIPTION"},
			{Header: "LIFECYCLE_MODE"},
			{Header: "ENABLED", AlignRight: true},
			{Header: "LOADED", AlignRight: true},
		}, rows)
		return nil
	},
}

func loadedPluginsFromDaemon(timeout time.Duration) (map[string]wire.PluginInfo, bool) {
	conn, err := net.DialTimeout("unix", wire.ResolveUISocketPath(), timeout)
	if err != nil {
		return nil, false
	}
	defer func() { _ = conn.Close() }()

	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: wire.MsgStatus, ClientID: "plugin-list"}); err != nil {
		return nil, false
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, false
	}

	var status wire.StatusResponse
	if err := wire.ReadMsg(wire.NewScanner(conn), &status); err != nil || status.Type != wire.MsgStatus {
		return nil, false
	}

	loaded := make(map[string]wire.PluginInfo, len(status.Plugins))
	for _, info := range status.Plugins {
		loaded[info.ID] = info
	}
	return loaded, true
}
