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
	cmd.AddCommand(newMilestoneListCmd())
	cmd.AddCommand(newMilestoneAttachCmd())
	cmd.AddCommand(newMilestoneDetachCmd())
	cmd.AddCommand(newMilestoneDeleteCmd())
	return cmd
}

func newMilestoneListCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a project's milestones, in position order",
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
			resp, raw, err := c.ListMilestones(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.MilestoneTable(cmd.OutOrStdout(), resp.Milestones)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
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

// newMilestoneDeleteCmd is `lode milestone delete`. Unlike `lode task delete`
// and `lode doc delete` this is a real delete rather than 044 §2's tombstone:
// a milestone holds a title, a position, and progress derived from children
// that survive it, so there is nothing a hidden row would preserve.
func newMilestoneDeleteCmd() *cobra.Command {
	var cascade bool
	cmd := &cobra.Command{
		Use:   "delete <milestone>",
		Short: "Delete an empty milestone, or --cascade one that still holds work",
		Long: "Delete a milestone. The row is removed, not tombstoned: what it held is\n" +
			"derived from children that outlive it.\n\n" +
			"A milestone that still holds tasks or deliverables is refused, naming the\n" +
			"counts. Detach them first, or pass --cascade, which deletes the attached\n" +
			"deliverables and detaches the attached tasks. Tasks are never deleted with\n" +
			"a milestone. Its own references to deliverables go with it either way.\n\n" +
			"The output names every id the delete detached or deleted. A milestone's\n" +
			"grouping is recorded nowhere else, so re-attaching from that list is the\n" +
			"only undo, and a deleted deliverable has none at all.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			deleted, raw, err := c.DeleteMilestone(cmd.Context(), args[0], cascade)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.MilestoneDeletionRender(cmd.OutOrStdout(), deleted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&cascade, "cascade", false,
		"delete the milestone's deliverables and detach its tasks, instead of refusing")
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
