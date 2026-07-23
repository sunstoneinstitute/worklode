package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Triage GitHub issues into tasks",
	}
	cmd.AddCommand(newInboxListCmd(), newInboxPromoteCmd(), newInboxDismissCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newInboxCmd())
}

func newInboxListCmd() *cobra.Command {
	var state string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List inbox issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ListIssues(cmd.Context(), state)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.IssueTable(cmd.OutOrStdout(), resp.Issues)
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "new", `triage state to list: "new", "promoted", "dismissed", or "" for all`)
	return cmd
}

// parseIssueNumber parses the <number> positional argument.
func parseIssueNumber(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid issue number %q: %w", s, err)
	}
	return n, nil
}

func newInboxPromoteCmd() *cobra.Command {
	var title, body, priority, kind, appliesTo string
	cmd := &cobra.Command{
		Use:   "promote <repo> <number>",
		Short: "Turn an inbox issue into a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := parseIssueNumber(args[1])
			if err != nil {
				return err
			}
			var versions []string
			if appliesTo != "" {
				versions = strings.Split(appliesTo, ",")
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.PromoteIssue(cmd.Context(), cli.PromoteInput{
				Repo: args[0], Number: number, Title: title, Body: body,
				Priority: priority, Kind: kind, AppliesToVersions: versions,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []cli.Task{t})
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "task title (default: the issue's title)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: critical, high, medium, low (required)")
	cmd.Flags().StringVar(&kind, "kind", "bug", "kind: feature, bug, chore, spec")
	cmd.Flags().StringVar(&appliesTo, "applies-to", "", "comma-separated versions this issue applies to, e.g. v1.2,v1.3")
	cmd.MarkFlagRequired("priority")
	return cmd
}

func newInboxDismissCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dismiss <repo> <number>",
		Short: "Dismiss an inbox issue without creating a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := parseIssueNumber(args[1])
			if err != nil {
				return err
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.DismissIssue(cmd.Context(), args[0], number)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "dismissed %s#%d\n", args[0], number)
			return nil
		},
	}
	return cmd
}
