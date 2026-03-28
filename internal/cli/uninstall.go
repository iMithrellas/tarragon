package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/spf13/cobra"
)

var uninstallPluginCmd = &cobra.Command{
	Use:   "uninstall-plugin <name>",
	Short: "Uninstall a plugin by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		pluginRoot := plugins.DefaultDir()
		if pluginRoot == "" {
			return errors.New("could not determine plugin installation directory")
		}

		pluginDir := filepath.Join(pluginRoot, name)
		if _, err := os.Stat(pluginDir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("plugin %q is not installed at %s", name, pluginDir)
			}
			return fmt.Errorf("access plugin directory: %w", err)
		}

		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Uninstalling plugin %q from %s\n", name, pluginDir); err != nil {
			return err
		}

		makefilePath := filepath.Join(pluginDir, "Makefile")
		if _, err := os.Stat(makefilePath); err == nil {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Running make uninstall..."); err != nil {
				return err
			}
			uninstallCmd := exec.Command("make", "uninstall")
			uninstallCmd.Dir = pluginDir
			uninstallCmd.Stdout = cmd.OutOrStdout()
			uninstallCmd.Stderr = cmd.ErrOrStderr()
			if runErr := uninstallCmd.Run(); runErr != nil {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: make uninstall failed (%v); removing directory directly...\n", runErr); err != nil {
					return err
				}
				if err := os.RemoveAll(pluginDir); err != nil {
					return fmt.Errorf("remove plugin directory after make uninstall failure: %w", err)
				}
			} else {
				if _, statErr := os.Stat(pluginDir); statErr == nil {
					if err := os.RemoveAll(pluginDir); err != nil {
						return fmt.Errorf("remove plugin directory after make uninstall: %w", err)
					}
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return fmt.Errorf("verify plugin directory after make uninstall: %w", statErr)
				}
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No Makefile found; removing plugin directory directly..."); err != nil {
				return err
			}
			if err := os.RemoveAll(pluginDir); err != nil {
				return fmt.Errorf("remove plugin directory: %w", err)
			}
		} else {
			return fmt.Errorf("stat Makefile: %w", err)
		}

		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Uninstalled plugin %q\n", name); err != nil {
			return err
		}
		return nil
	},
}
