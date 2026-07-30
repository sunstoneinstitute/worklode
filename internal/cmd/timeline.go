package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newTimelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timeline <task-id>",
		Short: "Show a task's full history: state changes, PRs, CI, reviews, deployments, runtime events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			resp, raw, err := c.Timeline(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TimelineRender(cmd.OutOrStdout(), resp.Timeline)
			return nil
		},
	}
	return cmd
}

func init() {
	rootCmd.AddCommand(newTimelineCmd())
}
