package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

var listPluginsCmd = &cobra.Command{
	Use:   "list-plugins",
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

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "NAME\tDESCRIPTION\tLIFECYCLE_MODE\tENABLED"); err != nil {
			return err
		}

		found := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			pluginTomlPath := filepath.Join(pluginRoot, entry.Name(), "plugin.toml")
			data, err := os.ReadFile(pluginTomlPath)
			if err != nil {
				continue
			}

			var cfg struct {
				Name          string `toml:"name"`
				Description   string `toml:"description"`
				LifecycleMode string `toml:"lifecycle_mode"`
				Enabled       bool   `toml:"enabled"`
			}
			if err := toml.Unmarshal(data, &cfg); err != nil {
				continue
			}

			if cfg.Name == "" {
				cfg.Name = entry.Name()
			}

			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%t\n", cfg.Name, cfg.Description, cfg.LifecycleMode, cfg.Enabled); err != nil {
				return err
			}
			found = true
		}

		if !found {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No plugins installed."); err != nil {
				return err
			}
			return nil
		}

		return w.Flush()
	},
}
