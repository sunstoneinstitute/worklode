package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// newOverviewCmd wires `lode overview` — the one-screen roll-up. `lode
// worktree status` is a different view and stays as it is.
func newOverviewCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "overview",
		Short: "One-screen roll-up: drift counts, gaps, frontier, critical head",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Overview(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.OverviewRender(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "roll up one project")
	return cmd
}

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

func init() {
	rootCmd.AddCommand(newOverviewCmd())
}
