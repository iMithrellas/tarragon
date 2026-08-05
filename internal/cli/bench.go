package cli

import (
	"time"

	"github.com/mithrel-dots/tarragon/internal/bench"
	"github.com/mithrel-dots/tarragon/internal/wire"
	"github.com/spf13/cobra"
)

var benchCmd = &cobra.Command{
	Use:          "bench",
	Short:        "Benchmark installed plugins through the daemon UI socket",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return bench.Run(bench.Options{
			RandomInputs: benchRandom,
			Iterations:   benchIterations,
			Timeout:      benchTimeout,
			Seed:         benchSeed,
			WorstPct:     benchWorstPct,
			Socket:       benchSocket,
		}, cmd.OutOrStdout())
	},
}

var (
	benchRandom     int
	benchIterations int
	benchTimeout    time.Duration
	benchSeed       int64
	benchWorstPct   float64
	benchSocket     string
)

func init() {
	benchCmd.Flags().IntVar(&benchRandom, "random", 100, "Number of random inputs")
	benchCmd.Flags().IntVar(&benchIterations, "iterations", 1, "Runs per input")
	benchCmd.Flags().DurationVar(&benchTimeout, "timeout", 2*time.Second, "Timeout per query")
	benchCmd.Flags().Int64Var(&benchSeed, "seed", 0, "Random seed (default: time-based)")
	benchCmd.Flags().Float64Var(&benchWorstPct, "worst-pct", 99, "Percentile for latency reporting")
	benchCmd.Flags().StringVar(&benchSocket, "socket", wire.SocketUI, "Daemon UI Unix socket path")
	rootCmd.AddCommand(benchCmd)
}
