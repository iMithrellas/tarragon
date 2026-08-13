package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion",
	Short: "Shell completion helpers",
}

var completionGenCmd = &cobra.Command{
	Use:   "generate [shell]",
	Short: "Generate a completion script to stdout (bash|zsh|fish|powershell)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sh := strings.ToLower(args[0])
		switch sh {
		case "bash":
			return rootCmd.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return rootCmd.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return rootCmd.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell", "pwsh", "ps":
			return rootCmd.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		default:
			return fmt.Errorf("unknown shell: %s (valid: bash, zsh, fish, powershell)", sh)
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
	completionCmd.AddCommand(completionGenCmd)
}
