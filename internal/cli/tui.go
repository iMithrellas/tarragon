package cli

import (
	"github.com/iMithrellas/tarragon/internal/ui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Run the text-based UI",
	Run: func(cmd *cobra.Command, args []string) {
		ui.RunTUI()
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
