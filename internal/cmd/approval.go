package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// newApprovalCmd is `lode approval`: ask for review, and see what is
// outstanding.
//
// There is no `lode approval approve` — nor reject, nor request-changes — and
// that is spec 029 §7.3, not an unfinished command family: approving is a web
// UI act because the OIDC session's group claims are fresh and a 30-day CLI
// token's are not. `lode approval list` prints the ids; the decision itself
// happens on the cockpit's /reviews page.
func newApprovalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approval",
		Short: "Request review on a document and see what review is outstanding",
		Long: "Request review on a document and see what review is outstanding.\n\n" +
			"Deciding an approval is a web UI act (spec 029 §7.3) and has no\n" +
			"command here: open the cockpit's Reviews page to approve, reject, or\n" +
			"request changes.",
	}
	cmd.AddCommand(newApprovalRequestCmd(), newApprovalListCmd())
	return cmd
}

func init() { rootCmd.AddCommand(newApprovalCmd()) }

// newApprovalRequestCmd opens one awaiting lane per reviewer on the
// document's current version (025 §7.3).
func newApprovalRequestCmd() *cobra.Command {
	var reviewers []string
	cmd := &cobra.Command{
		Use:   "request <id-or-slug>",
		Short: "Request approval on a document from one or more reviewers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(reviewers) == 0 {
				return fmt.Errorf("at least one --reviewer is required")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			id, err := resolveDocID(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			d, raw, err := c.RequestDocApproval(cmd.Context(), id, reviewers)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "requested approval on %s v%d from %s\n",
				cli.DocRef(d), d.Version, strings.Join(reviewers, ", "))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&reviewers, "reviewer", nil,
		"actor id whose approval the document needs (repeat the flag for each one; not comma-separated)")
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
