package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newDocProgressCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "progress",
		Short: "How much of each spec in a project exists, and what moves it next",
		Long: `Print the project's derived progress (066 §1): one line per spec, grouped
by the act that moves it next.

This is the same reading the cockpit's Progress page draws, so an agent
sees what a person sees. Nothing here is stored — every state is
recomputed from the covers edges, the plans and their minted tasks on
each call, so it cannot disagree with the run board or ` + "`lode doc todo`" + `.

` + "`lode doc todo <ref>`" + ` answers the same question for one spec, in more
detail; this is the whole project at a glance. There is no completion
percentage, by design.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errNoProject
			}
			p, raw, err := c.ProjectProgress(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ProgressTable(cmd.OutOrStdout(), p)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	return cmd
}
