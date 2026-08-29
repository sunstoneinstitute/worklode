package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newDocReviewersCmd is `lode doc reviewers`: shows or replaces a document's
// durable reviewer set (025 §7.3, WL-359) — the actors `lode approval
// request` opens an awaiting lane for on the document's current version. The
// set is not versioned: it survives an accept/revise cycle, which is what
// lets a review task minted for a §8.2 in-place amendment name "the original
// approvers" without the caller re-assigning them.
//
// There is no add/remove verb, matching --set: replacing the whole set is
// the operation, the same as `lode task edit --secrets` — "who reviews
// stays a social choice" (§7.3), decided afresh each time rather than
// accumulated a name at a time.
func newDocReviewersCmd() *cobra.Command {
	var set []string
	cmd := &cobra.Command{
		Use:   "reviewers <ref>",
		Short: "Show or replace a document's assigned reviewer set",
		Long: "Show or replace the durable reviewer set spec 025 §7.3 assigns to a\n" +
			"document (WL-359) — independent of any one revision, and what\n" +
			"`lode approval request` reads when it opens an awaiting lane per\n" +
			"reviewer.\n\n" +
			"With no flag, prints the current set and who still owes a review on\n" +
			"the document's current version. --set replaces the whole set; the\n" +
			"current owner or an admin may call it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("set") {
				d, raw, err := c.GetDoc(cmd.Context(), id)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				printDocReviewers(cmd, d.Doc)
				return nil
			}

			d, raw, err := c.SetDocReviewers(cmd.Context(), id, set)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "reviewers for %s: %s\n", cli.DocRef(d), reviewerList(d.Reviewers))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&set, "set", nil,
		"replace the reviewer set with these actor ids (comma-separated)")
	return cmd
}

// printDocReviewers renders the no-flag "show" form: the assigned set, and
// who still owes a review on the current version.
func printDocReviewers(cmd *cobra.Command, d model.Doc) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "reviewers for %s: %s\n", cli.DocRef(d), reviewerList(d.Reviewers))
	if len(d.ReviewersAwaiting) > 0 {
		fmt.Fprintf(out, "still owed on v%d: %s\n", d.Version, strings.Join(d.ReviewersAwaiting, ", "))
	}
}

// reviewerList renders an empty set distinctly from a real one, so "none
// assigned" cannot be misread as a fetch that silently returned nothing.
func reviewerList(reviewers []string) string {
	if len(reviewers) == 0 {
		return "(none assigned)"
	}
	return strings.Join(reviewers, ", ")
}
