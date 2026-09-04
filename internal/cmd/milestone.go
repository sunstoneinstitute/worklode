package cmd

import (
	"fmt"

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
	cmd.AddCommand(newMilestoneAttachCmd())
	cmd.AddCommand(newMilestoneDetachCmd())
	return cmd
}

// deliverableIDOrErr rejects a task id passed where a deliverable id was
// wanted: the two live behind different attach commands (029 §2), and a task
// id here is a common mistake worth naming rather than surfacing as a store
// 404.
func deliverableIDOrErr(id string) error {
	if taskID.MatchString(id) {
		return fmt.Errorf("%s is a task id; attach a task to a milestone with `lode task edit --milestone`", id)
	}
	return nil
}

func newMilestoneAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <milestone> <deliverable>",
		Short: "Attach a deliverable to a milestone in the same project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			milestone, deliverable := args[0], args[1]
			if err := deliverableIDOrErr(deliverable); err != nil {
				return err
			}
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			d, raw, err := c.SetDeliverableMilestone(cmd.Context(), deliverable, milestone)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DeliverableTable(cmd.OutOrStdout(), []model.Deliverable{d})
			return nil
		},
	}
	return cmd
}

func newMilestoneDetachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <deliverable>",
		Short: "Detach a deliverable from its milestone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deliverable := args[0]
			if err := deliverableIDOrErr(deliverable); err != nil {
				return err
			}
			c, _, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			d, raw, err := c.SetDeliverableMilestone(cmd.Context(), deliverable, "")
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DeliverableTable(cmd.OutOrStdout(), []model.Deliverable{d})
			return nil
		},
	}
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
