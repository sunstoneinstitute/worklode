package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newBoardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "board [project]",
		Short: "Show the task board: what's in progress, in review, blocked, and ready",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var project string
			if len(args) == 1 {
				project = args[0]
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.Board(cmd.Context(), project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.BoardRender(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	return cmd
}

func init() {
	rootCmd.AddCommand(newBoardCmd())
}
