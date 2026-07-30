package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newBoardCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "board [project]",
		Short: "Show the task board: what's in progress, in review, blocked, and ready",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			// The positional project is the older spelling of --project; it
			// wins over the resolution chain the same way the flag does.
			if len(args) == 1 {
				if cmd.Flags().Changed("project") || cmd.Flags().Changed("repo") {
					return errors.New("pass the project either positionally or with --project/--repo, not both")
				}
				if err := cmd.Flags().Set("project", args[0]); err != nil {
					return err
				}
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Board(cmd.Context(), sc.Project)
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
	addScopeFlags(cmd, &scope, "show one project's board")
	return cmd
}

func init() {
	rootCmd.AddCommand(newBoardCmd())
}
