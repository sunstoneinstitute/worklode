package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newMilestoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "milestone",
		Short: "Milestones: the ordered containers a project's tasks and deliverables hang off",
	}
	cmd.AddCommand(newMilestoneAddCmd())
	return cmd
}

func newMilestoneAddCmd() *cobra.Command {
	var scope scopeFlags
	var position int
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create a milestone",
		Args:  cobra.ExactArgs(1),
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
			m, raw, err := c.CreateMilestone(cmd.Context(), sc.Project,
				model.CreateMilestoneInput{Title: args[0], Position: position})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.MilestoneTable(cmd.OutOrStdout(), []model.Milestone{m})
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().IntVar(&position, "position", 0,
		"position in the project's milestone order (default: after the last one)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMilestoneCmd())
}
