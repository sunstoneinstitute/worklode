package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// newApprovalCmd is `lode approval`: require review, ask for it, see what is
// outstanding, and decide it.
func newApprovalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval",
		Short: "Require, request, list and decide approvals",
	}
	cmd.AddCommand(newApprovalAddCmd(), newApprovalRequestCmd(), newApprovalListCmd(),
		newApprovalDecideCmd())
	return cmd
}

// newApprovalAddCmd files one ad-hoc approval requirement (WL-REQ-124). It is
// `add`, L3's create verb, and not `require`: WL-REQ-305 L3 admits a new verb
// only for an act none of the seven expresses, and filing a requirement is
// creating an approval row.
//
// It is a different act from `approval request`, which materializes a
// document's own durable reviewer set (WL-REQ-170) and takes no policy of its
// own: this one names the target, the lane and who owes the decision.
func newApprovalAddCmd() *cobra.Command {
	var role, actor, lane, revision string
	cmd := &cobra.Command{
		Use:   "add <kind> <id>",
		Short: "Require an approval on one entity",
		Long: "Require an approval on one entity (WL-REQ-124): a review lane a\n" +
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
// document's durable reviewer set (WL-REQ-170) on its current version. The set
// itself is assigned separately, with `lode doc set reviewers` (WL-359,
// WL-487) — this command only materializes the lanes.
func newApprovalRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request <ref>",
		Short: "Open an approval lane for each of a document's assigned reviewers",
		Long: "Open an approval lane for each of a document's assigned reviewers,\n" +
			"on its current version (WL-REQ-1658). Assign the reviewer set first with\n" +
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

// newApprovalDecideCmd records a verdict on one approval (WL-REQ-1608). The
// decider is the token's actor; the server applies every store check.
func newApprovalDecideCmd() *cobra.Command {
	var verdict, note string
	cmd := &cobra.Command{
		Use:   "decide <id>",
		Short: "Approve, request changes on, or reject an approval",
		Long: "Approve, request changes on, or reject an approval (WL-REQ-1608).\n\n" +
			"<id> is the approval id `lode approval list` prints. --verdict is\n" +
			"approve, request_changes or reject. Deciding a row that needs a group\n" +
			"requires a login within the last 24 hours: run `lode login` if refused.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: awaitingApprovalIDs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("approval id %q is not a number", args[0])
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			a, raw, err := c.DecideApproval(cmd.Context(), id,
				model.DecideApprovalInput{Verdict: verdict, Note: note})
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
	cmd.Flags().StringVar(&verdict, "verdict", "", "approve, request_changes or reject")
	cmd.Flags().StringVar(&note, "note", "", "optional note recorded with the decision")
	_ = cmd.MarkFlagRequired("verdict")
	_ = cmd.RegisterFlagCompletionFunc("verdict", func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return staticCompletions([]string{"approve", "request_changes", "reject"}, toComplete)
	})
	return cmd
}

// awaitingApprovalIDs completes `approval decide`'s id from the awaiting
// queue, each candidate described by its entity's title.
func awaitingApprovalIDs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel, c, _, ok := completionClient(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer cancel()
	resp, _, err := c.ListApprovals(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, a := range resp.Approvals {
		id := strconv.FormatInt(a.ID, 10)
		if strings.HasPrefix(id, toComplete) {
			out = append(out, cobra.CompletionWithDesc(id, completionDescription(a.Title)))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
