package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

var enablePluginCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a system plugin by executable name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		if name == "" {
			return errors.New("plugin name is required")
		}

		binaryPath, err := resolveSystemBinary(name)
		if err != nil {
			return err
		}

		manifestBytes, err := fetchSystemPluginManifest(binaryPath)
		if err != nil {
			return err
		}

		rewrittenManifest, pluginName, err := rewriteManifestForSystem(manifestBytes, binaryPath, name)
		if err != nil {
			return err
		}

		pluginRoot := plugins.DefaultDir()
		if pluginRoot == "" {
			return errors.New("could not determine plugin installation directory")
		}
		if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
			return fmt.Errorf("create plugin root directory: %w", err)
		}

		pluginDir := filepath.Join(pluginRoot, pluginName)
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			return fmt.Errorf("create plugin directory: %w", err)
		}

		manifestPath := filepath.Join(pluginDir, "plugin.toml")
		if err := os.WriteFile(manifestPath, rewrittenManifest, 0o644); err != nil {
			return fmt.Errorf("write plugin manifest: %w", err)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Enabled system plugin %q using %s\n", pluginName, binaryPath)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote manifest to %s\n", manifestPath)
		return nil
	},
}

func resolveSystemBinary(name string) (string, error) {
	whichPath, err := exec.LookPath("which")
	if err != nil {
		return "", errors.New("which is required but was not found in PATH")
	}

	out, err := exec.Command(whichPath, name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve %q in PATH via which: %w", name, err)
	}

	binaryPath := strings.TrimSpace(string(out))
	if binaryPath == "" {
		return "", fmt.Errorf("resolve %q in PATH via which: empty output", name)
	}
	return binaryPath, nil
}

func fetchSystemPluginManifest(binaryPath string) ([]byte, error) {
	cmd := exec.Command(binaryPath, "tarragon", "manifest")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrMsg := strings.TrimSpace(stderr.String())
		if stderrMsg != "" {
			return nil, fmt.Errorf("run %q tarragon manifest: %w (stderr: %s)", binaryPath, err, stderrMsg)
		}
		return nil, fmt.Errorf("run %q tarragon manifest: %w", binaryPath, err)
	}

	manifest := bytes.TrimSpace(stdout.Bytes())
	if len(manifest) == 0 {
		return nil, errors.New("plugin manifest command returned empty output")
	}
	return manifest, nil
}

func rewriteManifestForSystem(manifest []byte, binaryPath string, fallbackName string) ([]byte, string, error) {
	var cfg plugins.PluginConfig
	if err := toml.Unmarshal(manifest, &cfg); err != nil {
		return nil, "", fmt.Errorf("parse plugin manifest: %w", err)
	}

	pluginName := strings.TrimSpace(cfg.Name)
	if pluginName == "" {
		pluginName = fallbackName
	}
	if strings.TrimSpace(cfg.Entrypoint) == "" {
		return nil, "", errors.New("plugin manifest is missing required field: entrypoint")
	}

	rewritten := manifest
	if !filepath.IsAbs(cfg.Entrypoint) {
		var found bool
		rewritten, found = setSimpleTOMLStringKey(rewritten, "entrypoint", binaryPath)
		if !found {
			rewritten = appendWithNewline(rewritten, "entrypoint = "+strconv.Quote(binaryPath))
		}
	}

	var sourceFound bool
	rewritten, sourceFound = setSimpleTOMLStringKey(rewritten, "source", "system")
	if !sourceFound {
		rewritten = appendWithNewline(rewritten, "source = \"system\"")
	}

	return rewritten, pluginName, nil
}

func setSimpleTOMLStringKey(content []byte, key string, value string) ([]byte, bool) {
	lines := strings.Split(string(content), "\n")
	replacement := strconv.Quote(value)

	for i, line := range lines {
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}

		if strings.TrimSpace(line[:idx]) != key {
			continue
		}

		comment := ""
		rhs := line[idx+1:]
		if commentIdx := strings.Index(rhs, "#"); commentIdx >= 0 {
			comment = strings.TrimSpace(rhs[commentIdx:])
		}

		newLine := line[:idx+1] + " " + replacement
		if comment != "" {
			newLine += " " + comment
		}

		lines[i] = newLine
		return []byte(strings.Join(lines, "\n")), true
	}

	return content, false
}

func appendWithNewline(content []byte, line string) []byte {
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	content = append(content, []byte(line)...)
	content = append(content, '\n')
	return content
}
