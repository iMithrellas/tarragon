package cli

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/iMithrellas/tarragon/internal/texttable"
	"github.com/iMithrellas/tarragon/internal/wire"
	"github.com/spf13/cobra"
)

// restartTimeout must comfortably exceed the daemon's plugin shutdown grace
// period, since the daemon only answers once every target has exited.
const restartTimeout = 30 * time.Second

var restartPluginCmd = &cobra.Command{
	Use:   "restart [plugin-name]",
	Short: "Restart plugin processes in the running daemon",
	Long: `Restart plugin processes in the running daemon.

Plugin manifests and configuration overrides are re-read first, so a restart
also picks up plugins installed or reconfigured since the daemon started.

With no argument every plugin is restarted. Behaviour depends on lifecycle:

  daemon                stopped and started again
  on_demand_persistent  stopped only; it starts again on the next matching query
  on_call               skipped; it has no long-lived process

Plugins are asked to exit with SIGTERM and are killed only if they overrun the
configured plugin_stop_timeout.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
			if err := ensurePluginExists(name); err != nil {
				return err
			}
		}

		resp, err := sendRestart(name)
		if err != nil {
			return fmt.Errorf("could not reach the daemon: %w (is it running?)", err)
		}

		if len(resp.Results) > 0 {
			rows := make([][]string, 0, len(resp.Results))
			for _, r := range resp.Results {
				rows = append(rows, []string{r.Name, r.Status, r.Message})
			}
			texttable.Render(cmd.OutOrStdout(), []texttable.Column{
				{Header: "NAME"},
				{Header: "STATUS"},
				{Header: "DETAIL"},
			}, rows)
		}

		if !resp.Success {
			return errors.New(resp.Message)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
		return nil
	},
}

func sendRestart(plugin string) (wire.RestartResponse, error) {
	var resp wire.RestartResponse

	conn, err := net.DialTimeout("unix", wire.SocketUI, 2*time.Second)
	if err != nil {
		return resp, err
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(restartTimeout)); err != nil {
		return resp, err
	}

	req := wire.UIRequest{Type: "restart", ClientID: "cli", Plugin: plugin}
	if err := wire.WriteMsg(conn, req); err != nil {
		return resp, err
	}

	if err := wire.ReadMsg(wire.NewScanner(conn), &resp); err != nil {
		return resp, err
	}
	return resp, nil
}
