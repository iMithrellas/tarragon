package cli

import (
	"time"

	"github.com/iMithrellas/tarragon/internal/ui"
	"github.com/spf13/cobra"
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Benchmark plugins with a TUI table",
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := ui.BenchOptions{
			RandomInputs: benchRandom,
			Iterations:   benchIterations,
			Timeout:      benchTimeout,
			Seed:         benchSeed,
			WorstPct:     benchWorstPct,
		}
		return ui.RunBenchTUI(opts)
	},
}

var (
	benchRandom     int
	benchIterations int
	benchTimeout    time.Duration
	benchSeed       int64
	benchWorstPct   float64
)

func init() {
	benchCmd.Flags().IntVar(&benchRandom, "random", 100, "Number of random inputs")
	benchCmd.Flags().IntVar(&benchIterations, "iterations", 1, "Runs per input")
	benchCmd.Flags().DurationVar(&benchTimeout, "timeout", 2*time.Second, "Timeout per query")
	benchCmd.Flags().Int64Var(&benchSeed, "seed", 0, "Random seed (default: time-based)")
	benchCmd.Flags().Float64Var(&benchWorstPct, "worst-pct", 99, "Percentile for worst inputs")
	rootCmd.AddCommand(benchCmd)
}
