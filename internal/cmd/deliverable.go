package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newDeliverableCmd groups the deliverable subcommands (spec 029 §3.1): a
// declared, checkable output of a project. Only the artifact-address form
// exists so far — WL-582's label form adds a column and a --label flag on
// top of this, nothing here has to be undone for that.
func newDeliverableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable",
		Short: "Deliverables: a project's declared, checkable outputs",
	}
	cmd.AddCommand(newDeliverableListCmd(), newDeliverableAddCmd())
	return cmd
}

func newDeliverableListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "list <project>",
		ValidArgsFunction: projectKeyAt(0),
		Short:             "List a project's deliverables",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ListDeliverables(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.DeliverableTable(cmd.OutOrStdout(), resp.Deliverables)
			return nil
		},
	}
	return cmd
}

func newDeliverableAddCmd() *cobra.Command {
	var description, deliverableURL, artifact, milestone string
	cmd := &cobra.Command{
		Use:               "add <project> <name>",
		ValidArgsFunction: projectKeyAt(0),
		Short:             "Declare a deliverable on a project",
		Args:              cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			d, raw, err := c.CreateDeliverable(cmd.Context(), args[0], model.CreateDeliverableInput{
				Name:        args[1],
				Description: description,
				URL:         deliverableURL,
				Artifact:    artifact,
				Milestone:   milestone,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "declared %s (%s)\n", d.ID, d.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "description of the deliverable")
	cmd.Flags().StringVar(&deliverableURL, "url", "", "a browser link for the deliverable")
	cmd.Flags().StringVar(&artifact, "artifact", "", "the artifact address this deliverable is verified by (e.g. bigquery://p/d/t)")
	cmd.Flags().StringVar(&milestone, "milestone", "", "attach the deliverable to a milestone in the same project")
	return cmd
}

func init() {
	rootCmd.AddCommand(newDeliverableCmd())
}
