package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

var installPluginCmd = &cobra.Command{
	Use:   "install-plugin <git-url>",
	Short: "Install a plugin from a git repository",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		gitURL := args[0]

		if _, err := exec.LookPath("git"); err != nil {
			return errors.New("git is required but was not found in PATH")
		}

		if _, err := exec.LookPath("make"); err != nil {
			return errors.New("make is required but was not found in PATH")
		}

		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "[1/6] Creating temporary directory..."); err != nil {
			return err
		}
		tmpDir, err := os.MkdirTemp("", "tarragon-plugin-*")
		if err != nil {
			return fmt.Errorf("create temp directory: %w", err)
		}
		defer func() {
			_ = os.RemoveAll(tmpDir)
		}()

		repoDir := filepath.Join(tmpDir, "repo")
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "[2/6] Cloning repository: %s\n", gitURL); err != nil {
			return err
		}
		cloneCmd := exec.Command("git", "clone", "--depth", "1", gitURL, repoDir)
		cloneCmd.Stdout = cmd.OutOrStdout()
		cloneCmd.Stderr = cmd.ErrOrStderr()
		if err := cloneCmd.Run(); err != nil {
			return fmt.Errorf("failed to clone repository: %w", err)
		}

		pluginTomlPath := filepath.Join(repoDir, "plugin.toml")
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "[3/6] Validating plugin.toml..."); err != nil {
			return err
		}
		if _, err := os.Stat(pluginTomlPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return errors.New("missing plugin.toml in repository root")
			}
			return fmt.Errorf("stat plugin.toml: %w", err)
		}

		data, err := os.ReadFile(pluginTomlPath)
		if err != nil {
			return fmt.Errorf("read plugin.toml: %w", err)
		}

		var cfg struct {
			Name string `toml:"name"`
		}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parse plugin.toml: %w", err)
		}
		if cfg.Name == "" {
			return errors.New("plugin.toml is missing required field: name")
		}

		makefilePath := filepath.Join(repoDir, "Makefile")
		if _, err := os.Stat(makefilePath); err == nil {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "[4/6] Running dependency checks (make check-deps)..."); err != nil {
				return err
			}
			checkCmd := exec.Command("make", "check-deps")
			checkCmd.Dir = repoDir
			checkCmd.Stdout = cmd.OutOrStdout()
			checkCmd.Stderr = cmd.ErrOrStderr()
			if err := checkCmd.Run(); err != nil {
				if _, werr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: dependency checks failed: %v\n", err); werr != nil {
					return werr
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat Makefile: %w", err)
		} else {
			return errors.New("plugin repository is missing Makefile; cannot run make install")
		}

		pluginRoot := plugins.DefaultDir()
		if pluginRoot == "" {
			return errors.New("could not determine plugin installation directory")
		}
		installRoot := filepath.Join(pluginRoot, cfg.Name)
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "[5/6] Building and installing plugin to %s ...\n", installRoot); err != nil {
			return err
		}
		if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
			return fmt.Errorf("create plugin root directory: %w", err)
		}

		installCmd := exec.Command("make", "install", fmt.Sprintf("INSTALL_ROOT=%s", installRoot), fmt.Sprintf("PLUGIN_DIR=%s", installRoot))
		installCmd.Dir = repoDir
		installCmd.Stdout = cmd.OutOrStdout()
		installCmd.Stderr = cmd.ErrOrStderr()
		if err := installCmd.Run(); err != nil {
			return fmt.Errorf("make install failed: %w", err)
		}
		if _, err := os.Stat(installRoot); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("make install completed but plugin directory was not created at %s", installRoot)
			}
			return fmt.Errorf("verify installed plugin directory: %w", err)
		}

		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "[6/6] Cleaning up temporary files..."); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q at %s\n", cfg.Name, installRoot); err != nil {
			return err
		}

		return nil
	},
}
