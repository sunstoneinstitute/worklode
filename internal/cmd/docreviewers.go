package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newDocReviewersCmd is `lode doc reviewers`: the read-only view of a
// document's durable reviewer set (025 §7.3, WL-359) — the actors `lode
// approval request` opens an awaiting lane for on the document's current
// version. The set is not versioned: it survives an accept/revise cycle,
// which is what lets a review task minted for a §8.2 in-place amendment name
// "the original approvers" without the caller re-assigning them.
//
// A view never writes (061 §1 rule L6): the paired write is `lode doc set
// reviewers <reviewer…> <ref>` (WL-487), matching `lode task set skills`.
func newDocReviewersCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "reviewers <ref>",
		ValidArgsFunction: docRefAt(0),
		Short:             "Show a document's assigned reviewer set",
		Long: "Show the durable reviewer set spec 025 §7.3 assigns to a document\n" +
			"(WL-359) — independent of any one revision, and what `lode approval\n" +
			"request` reads when it opens an awaiting lane per reviewer.\n\n" +
			"Prints the current set and who still owes a review on the document's\n" +
			"current version. The paired write is `lode doc set reviewers`.",
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
		},
	}
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

// docSetFields are the fields `lode doc set` writes — the switch below and
// the unknown-field error read this list, and so does completion (061 §1 L4).
var docSetFields = []string{"reviewers"}

// newDocSetCmd is `lode doc set <field> <value…> <ref>` (061 §2.1, WL-487):
// write one named field on a document. The field and the values are
// arguments, not part of the verb, matching `lode task set` — this is the
// doc half of the same rename `lode task set skills` made for tasks, and the
// two commands are shaped identically so they don't surprise each other.
//
// The document ref is always the LAST argument, matching `lode task set`, so
// a field can take more than one value: "reviewers" takes any number of
// actor ids, and naming none clears the set.
func newDocSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "set <field> <value…> <ref>",
		ValidArgsFunction: docSetArgs,
		Short:             "Set one field on a document, e.g. `lode doc set reviewers alice bob rev-spec`",
		Long: `Set one named field on a document. The document ref is always the last
argument; everything between the field and the ref is the value.

  reviewers  any number of actor ids, replacing whatever was assigned:
               lode doc set reviewers alice bob rev-spec
             naming none clears the document's reviewer set:
               lode doc set reviewers rev-spec`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			field, values, ref := args[0], args[1:len(args)-1], args[len(args)-1]
			switch field {
			case "reviewers":
			default:
				return fmt.Errorf("unknown field %q: settable fields are %s", field, strings.Join(docSetFields, ", "))
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, ref)
			if err != nil {
				return err
			}
			d, raw, err := c.SetDocReviewers(cmd.Context(), id, values)
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
}
