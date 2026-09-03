package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// newCriticalPathCmd wires `lode task critical-path [--task <id>]`; cycles
// are findings, not silent drops (spec 007 §Cycle handling). --task narrows
// the table to that task's row (its depth and fan-out), client-side, so
// --json re-encodes the narrowed value rather than passing the server's body
// through.
func newCriticalPathCmd() *cobra.Command {
	var task string
	cmd := &cobra.Command{
		Use:   "critical-path",
		Short: "Estimate-free critical path over blocks + requires (D12)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			resp, raw, err := c.CriticalPath(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				if task == "" {
					printRaw(cmd, raw)
					return nil
				}
				return printJSON(cmd, cli.CriticalPathFiltered(resp, task))
			}
			cli.CriticalPathRender(cmd.OutOrStdout(), resp, task)
			return nil
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "show only this task's criticality")
	return cmd
}
