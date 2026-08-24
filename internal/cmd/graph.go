package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "The knowledge-graph projection: its health, and what it owes",
	}
	cmd.AddCommand(newGraphProjectionCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newGraphCmd()) }

func newGraphProjectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projection",
		Short: "The backbone→graph projector's state",
	}
	cmd.AddCommand(newGraphProjectionStatusCmd())
	return cmd
}

func newGraphProjectionStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the projects the projector has quarantined, since when, and why",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ProjectionFailures(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProjectionFailureTable(cmd.OutOrStdout(), resp.Failures)
			return nil
		},
	}
}
