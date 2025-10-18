package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Shell completion helpers",
}

var completionGenCmd = &cobra.Command{
	Use:   "generate [shell]",
	Short: "Generate completion scripts (bash|zsh|fish|powershell)",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := completionsDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}

		shells := args
		if len(shells) == 0 {
			shells = []string{"bash", "zsh", "fish", "powershell"}
		}

		for _, sh := range shells {
			sh = strings.ToLower(sh)
			switch sh {
			case "bash":
				p := filepath.Join(target, "tarragon.bash")
				if err := writeBashCompletion(p); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), bashInstruction(p)); err != nil {
					return err
				}
			case "zsh":
				p := filepath.Join(target, "tarragon.zsh")
				if err := writeZshCompletion(p); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), zshInstruction(p)); err != nil {
					return err
				}
			case "fish":
				p := filepath.Join(target, "tarragon.fish")
				if err := writeFishCompletion(p); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), fishInstruction(p)); err != nil {
					return err
				}
			case "powershell", "pwsh", "ps":
				p := filepath.Join(target, "tarragon.ps1")
				if err := writePSCompletion(p); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), psInstruction(p)); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown shell: %s (valid: bash, zsh, fish, powershell)", sh)
			}
		}
		return nil
	},
}

func writeBashCompletion(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return rootCmd.GenBashCompletion(f)
}

func writeZshCompletion(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return rootCmd.GenZshCompletion(f)
}

func writeFishCompletion(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return rootCmd.GenFishCompletion(f, true)
}

func writePSCompletion(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return rootCmd.GenPowerShellCompletionWithDesc(f)
}

func completionsDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "tarragon", "completions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "tarragon", "completions"), nil
}

func bashInstruction(p string) string {
	base := "${XDG_DATA_HOME:-$HOME/.local/share}/tarragon/completions/tarragon.bash"
	return fmt.Sprintf("bash: add to ~/.bashrc\nsource %s", base)
}
func zshInstruction(p string) string {
	base := "${XDG_DATA_HOME:-$HOME/.local/share}/tarragon/completions/tarragon.zsh"
	return fmt.Sprintf("zsh: add to ~/.zshrc\nsource %s", base)
}
func fishInstruction(p string) string {
	base := "${XDG_DATA_HOME:-$HOME/.local/share}/tarragon/completions/tarragon.fish"
	return fmt.Sprintf("fish: add to ~/.config/fish/config.fish\nsource %s", base)
}
func psInstruction(p string) string {
	return "powershell: add to $PROFILE\nif ($env:XDG_DATA_HOME) { . \"$env:XDG_DATA_HOME/tarragon/completions/tarragon.ps1\" } else { . \"$HOME/.local/share/tarragon/completions/tarragon.ps1\" }"
}

func init() {
	rootCmd.AddCommand(completionCmd)
	completionCmd.AddCommand(completionGenCmd)
}
