package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Triage GitHub issues into tasks",
	}
	cmd.AddCommand(newInboxListCmd(), newInboxPromoteCmd(), newInboxDismissCmd(),
		newInboxLinkCmd(), newInboxImportCmd())
	return cmd
}

func init() {
	rootCmd.AddCommand(newInboxCmd())
}

func newInboxListCmd() *cobra.Command {
	var scope scopeFlags
	var state string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List inbox issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.ListIssues(cmd.Context(), state, sc.Project)
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
	addScopeFlags(cmd, &scope, "filter by project id")
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
	var title, body, priority, kind, appliesTo, parent string
	var draft bool
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
				Priority: priority, Kind: kind, AppliesToVersions: versions, Draft: draft,
				Parent: parent,
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
	cmd.Flags().BoolVar(&draft, "draft", false, "create the task as a draft (not claimable until `lode task ready`)")
	cmd.Flags().StringVar(&parent, "parent", "", "make the new task a child of this epic")
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

func newInboxLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <repo> <number> <task-id>",
		Short: "Attach an inbox issue to a task that already exists",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			number, err := parseIssueNumber(args[1])
			if err != nil {
				return err
			}
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			taskID, err := resolveTaskID(cmd.Context(), args[2], c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.LinkIssue(cmd.Context(), args[0], number, taskID)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "linked %s#%d to %s\n", args[0], number, taskID)
			return nil
		},
	}
	return cmd
}

func newInboxImportCmd() *cobra.Command {
	var state, since string
	var includePRs, dryRun bool
	cmd := &cobra.Command{
		Use:   "import <repo>",
		Short: "Backfill an inbox from a repo's existing GitHub issues",
		Long: "Pages the GitHub REST API and upserts through the same path the webhooks use.\n" +
			"Re-running is safe and leaves already-triaged issues alone.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := cli.ImportInput{
				Repo: args[0], State: state, IncludePRs: includePRs, DryRun: dryRun,
			}
			if since != "" {
				t, err := time.Parse(time.RFC3339, since)
				if err != nil {
					return fmt.Errorf("invalid --since %q: want RFC3339, e.g. 2026-01-01T00:00:00Z: %w", since, err)
				}
				in.Since = &t
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			res, raw, err := c.ImportInbox(cmd.Context(), in)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			prefix := ""
			if res.DryRun {
				prefix = "would import: "
			}
			fmt.Fprintf(out, "%s%s: %d new, %d updated issues; %d new, %d updated PRs\n",
				prefix, res.Repo, res.Issues.New, res.Issues.Updated, res.PRs.New, res.PRs.Updated)
			if res.Truncated {
				if res.NewestUpdatedAt != nil {
					fmt.Fprintf(out, "warning: hit the page cap; re-run with --since %s to continue\n",
						res.NewestUpdatedAt.UTC().Format(time.RFC3339))
				} else {
					fmt.Fprintf(out, "warning: hit the page cap; re-run with --since to get the rest\n")
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "open", `which to import: "open", "closed", or "all"`)
	cmd.Flags().BoolVar(&includePRs, "include-prs", false, "also import pull requests")
	cmd.Flags().StringVar(&since, "since", "", "only items updated at or after this RFC3339 time")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be imported without writing")
	return cmd
}
