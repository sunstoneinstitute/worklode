package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newApprovalCmd is `lode approval`: require review, ask for it, and see what
// is outstanding.
//
// There is no `lode approval approve` — nor reject, nor request-changes — and
// that is spec 029 §7.3, not an unfinished command family: approving is a web
// UI act because the OIDC session's group claims are fresh and a 30-day CLI
// token's are not. `lode approval list` prints the ids; the decision itself
// happens on the cockpit's /reviews page.
func newApprovalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval",
		Short: "Require and request review, and see what review is outstanding",
		Long: "Require and request review, and see what review is outstanding.\n\n" +
			"Deciding an approval is a web UI act (spec 029 §7.3) and has no\n" +
			"command here: open the cockpit's Reviews page to approve, reject, or\n" +
			"request changes.",
	}
	cmd.AddCommand(newApprovalAddCmd(), newApprovalRequestCmd(), newApprovalListCmd())
	return cmd
}

// newApprovalAddCmd files one ad-hoc approval requirement (029 §7.2). It is
// `add`, L3's create verb, and not `require`: 061 §1 L3 admits a new verb
// only for an act none of the seven expresses, and filing a requirement is
// creating an approval row.
//
// It is a different act from `approval request`, which materializes a
// document's own durable reviewer set (025 §7.3) and takes no policy of its
// own: this one names the target, the lane and who owes the decision.
func newApprovalAddCmd() *cobra.Command {
	var role, actor, lane, revision string
	cmd := &cobra.Command{
		Use:   "add <kind> <id>",
		Short: "Require an approval on one entity",
		Long: "Require an approval on one entity (029 §7.2): a review lane a\n" +
			"project's flow did not demand, filed by hand.\n\n" +
			"<kind> is " + strings.Join(model.ApprovalEntityKinds, ", ") + " and <id> is that\n" +
			"entity's id — a task id, a deliverable id, a \"repo#number\" pull\n" +
			"request, or any document reference for a doc. --role demands a\n" +
			"group and --actor a named person; naming both is an error. A\n" +
			"document requirement binds to the document's current version\n" +
			"unless --revision says otherwise. Re-filing the same requirement\n" +
			"returns the row already there.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: approvalTargetArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			kind, id := args[0], args[1]
			// A document's entity_id is model.DocEntityID's "doc:<id>", so
			// the argument is any document reference and this resolves it —
			// the same courtesy every other doc-taking command does.
			if kind == "doc" && !strings.HasPrefix(id, "doc:") {
				n, err := resolveDocID(cmd.Context(), c, id)
				if err != nil {
					return err
				}
				id = model.DocEntityID(n)
			}
			a, raw, err := c.RequireApproval(cmd.Context(), model.RequireApprovalInput{
				EntityKind: kind, EntityID: id, Revision: revision,
				Lane: lane, Role: role, Actor: actor,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ApprovalRender(cmd.OutOrStdout(), a)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "group that owes the decision")
	cmd.Flags().StringVar(&actor, "actor", "", "actor that owes the decision")
	cmd.Flags().StringVar(&lane, "lane", "", "lane the requirement occupies (default: the no-lane row)")
	cmd.Flags().StringVar(&revision, "revision", "", "revision the requirement binds to")
	return cmd
}

// approvalTargetArgs completes `approval add`'s two positional arguments:
// the entity kind, then whichever ids that kind has a helper for. A
// deliverable or a pull request completes nothing — neither has one.
func approvalTargetArgs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch {
	case len(args) == 0:
		return staticCompletions(model.ApprovalEntityKinds, toComplete)
	case args[0] == "task":
		return taskIDAt(1)(cmd, args, toComplete)
	case args[0] == "doc":
		return docRefAt(1)(cmd, args, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func init() { rootCmd.AddCommand(newApprovalCmd()) }

// newApprovalRequestCmd opens one awaiting lane per reviewer in the
// document's durable reviewer set (025 §7.3) on its current version. The set
// itself is assigned separately, with `lode doc set reviewers` (WL-359,
// WL-487) — this command only materializes the lanes.
func newApprovalRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request <ref>",
		Short: "Open an approval lane for each of a document's assigned reviewers",
		Long: "Open an approval lane for each of a document's assigned reviewers,\n" +
			"on its current version (025 §7.3). Assign the reviewer set first with\n" +
			"`lode doc set reviewers <actor> <actor> ... <ref>`; re-running this\n" +
			"after a later `lode doc set reviewers` call adds only the newly\n" +
			"assigned lanes.",
		Args: cobra.ExactArgs(1),
		// resolveDocID below takes any document reference, so the argument is
		// spelled and completed like every other one: `<ref>`, docRefAt.
		ValidArgsFunction: docRefAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.RequestDocApproval(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "requested approval on %s v%d from %s\n",
				cli.DocRef(d), d.Version, strings.Join(d.Reviewers, ", "))
			return nil
		},
	}
	return cmd
}

// newApprovalListCmd prints the awaiting queue, oldest first.
func newApprovalListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every outstanding approval, oldest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ListApprovals(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.ApprovalTable(cmd.OutOrStdout(), resp.Approvals)
			return nil
		},
	}
}
