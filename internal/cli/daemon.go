package cli

import (
    "github.com/iMithrellas/tarragon/internal/daemon"
    "github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
    Use:   "daemon",
    Short: "Run the Tarragon daemon",
    Run: func(cmd *cobra.Command, args []string) {
        daemon.RunDaemon()
    },
}

func init() {
    rootCmd.AddCommand(daemonCmd)
}
