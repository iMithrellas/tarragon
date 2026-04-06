package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/iMithrellas/tarragon/internal/config"
	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
	"github.com/spf13/cobra"
)

var (
	pluginConfigEnabled   string
	pluginConfigPrefix    string
	pluginConfigLifecycle string
	pluginConfigReset     bool
)

type pluginConfigRow struct {
	Name        string
	Enabled     bool
	Lifecycle   plugins.LifecycleMode
	Prefix      string
	Description string
	EnabledOv   bool
	LifecycleOv bool
	PrefixOv    bool
}

var pluginConfigCmd = &cobra.Command{
	Use:   "config [plugin-name]",
	Short: "View or override plugin configuration",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setEnabled := cmd.Flags().Changed("enabled")
		setPrefix := cmd.Flags().Changed("prefix")
		setLifecycle := cmd.Flags().Changed("lifecycle")
		setReset := cmd.Flags().Changed("reset") && pluginConfigReset

		if len(args) == 0 {
			if setEnabled || setPrefix || setLifecycle || setReset {
				return errors.New("plugin name is required when setting or resetting overrides")
			}
			return printPluginConfigTable(cmd, "")
		}

		name := args[0]
		if err := ensurePluginExists(name); err != nil {
			return err
		}

		if !setEnabled && !setPrefix && !setLifecycle && !setReset {
			return printPluginConfigTable(cmd, name)
		}

		if setReset && (setEnabled || setPrefix || setLifecycle) {
			return errors.New("--reset cannot be combined with other override flags")
		}

		if setReset {
			if err := config.ResetPluginOverride(name); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Reset overrides for plugin %q\n", name)
			if ok, msg, err := sendReload(); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not notify daemon: %v\n", err)
			} else if !ok {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: daemon reload failed: %s\n", msg)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Daemon reloaded successfully\n")
			}
			return nil
		}

		overrides := make(map[string]any)
		if setEnabled {
			parsed, err := strconv.ParseBool(strings.TrimSpace(pluginConfigEnabled))
			if err != nil {
				return fmt.Errorf("invalid --enabled value %q (expected true or false)", pluginConfigEnabled)
			}
			overrides["enabled"] = parsed
		}

		if setPrefix {
			overrides["prefix"] = pluginConfigPrefix
		}

		if setLifecycle {
			mode, err := plugins.ParseLifecycleMode(strings.TrimSpace(pluginConfigLifecycle))
			if err != nil {
				return fmt.Errorf("invalid --lifecycle value: %w", err)
			}
			overrides["lifecycle_mode"] = string(mode)
		}

		if len(overrides) == 0 {
			return errors.New("at least one override flag is required")
		}

		if err := config.WritePluginOverride(name, overrides); err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated overrides for plugin %q\n", name)
		if ok, msg, err := sendReload(); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not notify daemon: %v\n", err)
		} else if !ok {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: daemon reload failed: %s\n", msg)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Daemon reloaded successfully\n")
		}
		return nil
	},
}

func sendReload() (bool, string, error) {
	conn, err := net.DialTimeout("unix", wire.SocketUI, 2*time.Second)
	if err != nil {
		return false, "", err
	}
	defer func() {
		_ = conn.Close()
	}()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	req := wire.UIRequest{Type: "reload", ClientID: "cli"}
	if err := wire.WriteMsg(conn, req); err != nil {
		return false, "", err
	}

	scanner := wire.NewScanner(conn)
	var resp wire.ReloadResponse
	if err := wire.ReadMsg(scanner, &resp); err != nil {
		return false, "", err
	}

	return resp.Success, resp.Message, nil
}

func init() {
	pluginConfigCmd.Flags().StringVar(&pluginConfigEnabled, "enabled", "", "Override plugin enabled state (true/false)")
	pluginConfigCmd.Flags().StringVar(&pluginConfigPrefix, "prefix", "", "Override plugin prefix")
	pluginConfigCmd.Flags().StringVar(&pluginConfigLifecycle, "lifecycle", "", "Override plugin lifecycle (daemon|on_demand_persistent|on_call)")
	pluginConfigCmd.Flags().BoolVar(&pluginConfigReset, "reset", false, "Reset all overrides for this plugin")
}

func ensurePluginExists(name string) error {
	pluginRoot := plugins.DefaultDir()
	if pluginRoot == "" {
		return errors.New("could not determine plugin installation directory")
	}

	mgr := plugins.NewManager(pluginRoot)
	if err := mgr.Discover(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("plugin %q not found", name)
		}
		return err
	}

	if _, ok := mgr.Plugins[name]; !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	return nil
}

func printPluginConfigTable(cmd *cobra.Command, onlyName string) error {
	rows, err := collectPluginConfigRows()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No plugins installed.")
			return nil
		}
		return err
	}

	if len(rows) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No plugins installed.")
		return nil
	}

	filtered := rows
	if onlyName != "" {
		filtered = nil
		for _, row := range rows {
			if row.Name == onlyName {
				filtered = append(filtered, row)
				break
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("plugin %q not found", onlyName)
		}
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tENABLED\tLIFECYCLE\tPREFIX\tDESCRIPTION"); err != nil {
		return err
	}
	for _, row := range filtered {
		enabled := fmt.Sprintf("%t", row.Enabled)
		if row.EnabledOv {
			enabled += "*"
		}
		lifecycle := string(row.Lifecycle)
		if row.LifecycleOv {
			lifecycle += "*"
		}
		prefix := row.Prefix
		if row.PrefixOv {
			prefix += "*"
		}

		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", row.Name, enabled, lifecycle, prefix, row.Description); err != nil {
			return err
		}
	}

	return w.Flush()
}

func collectPluginConfigRows() ([]pluginConfigRow, error) {
	pluginRoot := plugins.DefaultDir()
	if pluginRoot == "" {
		return nil, errors.New("could not determine plugin installation directory")
	}

	baseMgr := plugins.NewManager(pluginRoot)
	if err := baseMgr.Discover(); err != nil {
		return nil, err
	}

	effectiveMgr := plugins.NewManager(pluginRoot)
	if err := effectiveMgr.Discover(); err != nil {
		return nil, err
	}
	if err := effectiveMgr.ApplyOverrides(); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(baseMgr.Plugins))
	for name := range baseMgr.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]pluginConfigRow, 0, len(names))
	for _, name := range names {
		base := baseMgr.Plugins[name]
		eff := effectiveMgr.Plugins[name]
		if base == nil || eff == nil {
			continue
		}
		rows = append(rows, pluginConfigRow{
			Name:        name,
			Enabled:     eff.Config.Enabled,
			Lifecycle:   eff.Config.Lifecycle,
			Prefix:      eff.Config.Prefix,
			Description: eff.Config.Description,
			EnabledOv:   eff.Config.Enabled != base.Config.Enabled,
			LifecycleOv: eff.Config.Lifecycle != base.Config.Lifecycle,
			PrefixOv:    eff.Config.Prefix != base.Config.Prefix,
		})
	}

	return rows, nil
}
