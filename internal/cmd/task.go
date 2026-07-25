package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Create, inspect, and drive tasks through their lifecycle",
	}
	cmd.AddCommand(
		newTaskAddCmd(),
		newTaskListCmd(),
		newTaskShowCmd(),
		newTaskEditCmd(),
		newTaskReadyCmd(),
		newTaskReopenCmd(),
		newTaskReworkCmd(),
		newTaskClaimCmd(),
		newTaskRenewCmd(),
		newTaskReleaseCmd(),
		newTaskDoneCmd(),
		newTaskAbandonCmd(),
		newTaskBlockCmd(),
		newTaskUnblockCmd(),
		newTaskBriefCmd(),
	)
	return cmd
}

func init() {
	rootCmd.AddCommand(newTaskCmd())
}

func newTaskAddCmd() *cobra.Command {
	var project, title, body, priority, kind, concern string
	var draft bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			project := resolveProject(cmd, project, cfg.CurrentProject)
			if project == "" {
				return errors.New(`--project is required (or set current_project in .worklode/config.toml or ~/.config/worklode/config.toml)`)
			}
			t, raw, err := c.CreateTask(cmd.Context(), cli.CreateTaskInput{
				Project: project, Title: title, Body: body, Priority: priority, Kind: kind,
				Concern: concern, Draft: draft,
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
	cmd.Flags().StringVar(&project, "project", "", "project id"+projectFlagUsage)
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&priority, "priority", "medium", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kind, "kind", "feature", "kind: feature, bug, chore, spec")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security (optional)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as draft (not claimable until published with `lode task ready`)")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var project, priority string
	var statuses []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (delivered and abandoned are hidden unless requested with --status)",
		RunE: func(cmd *cobra.Command, args []string) error {
			states := resolveStatusFilter(statuses)
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			resp, raw, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
				Project: resolveProject(cmd, project, cfg.CurrentProject), States: states, Priority: priority,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), resp.Tasks)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "filter by project id"+projectFlagUsage+"; pass --project= for all projects")
	cmd.Flags().StringArrayVar(&statuses, "status", nil, "filter by status: draft, ready, in_progress, in_review, merged, deployed_dev, deployed_prod, released, abandoned, or all (repeatable; default hides merged, deployed_dev, deployed_prod, released, and abandoned)")
	cmd.Flags().StringVar(&priority, "priority", "", "filter by priority")
	return cmd
}

// resolveStatusFilter turns `lode task list --status` values into the state
// filter sent to the server: no flag hides the delivered states (merged,
// deployed_dev, deployed_prod, released) and abandoned; "all" disables
// filtering entirely.
func resolveStatusFilter(statuses []string) []string {
	if len(statuses) == 0 {
		return []string{"draft", "ready", "in_progress", "in_review"}
	}
	var states []string
	for _, s := range statuses {
		for _, part := range strings.Split(s, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if part == "all" {
				return nil
			}
			states = append(states, part)
		}
	}
	return states
}

func newTaskShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task's details: body, edges, blocked status, and lease holder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.GetTask(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskDetailRender(cmd.OutOrStdout(), t)
			return nil
		},
	}
	return cmd
}

func newTaskEditCmd() *cobra.Command {
	var concern, priority string
	var needsDecomposition bool
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task's concern, priority, or needs-decomposition flag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var in cli.EditTaskInput
			if cmd.Flags().Changed("concern") {
				in.Concern = &concern
			}
			if cmd.Flags().Changed("priority") {
				in.Priority = &priority
			}
			if cmd.Flags().Changed("needs-decomposition") {
				in.NeedsDecomposition = &needsDecomposition
			}
			if in.Concern == nil && in.Priority == nil && in.NeedsDecomposition == nil {
				return fmt.Errorf("nothing to edit: set --concern, --priority, or --needs-decomposition")
			}

			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.EditTask(cmd.Context(), args[0], in)
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
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security, or none to clear")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: critical, high, medium, low")
	cmd.Flags().BoolVar(&needsDecomposition, "needs-decomposition", false, "mark (or unmark) the task as needing decomposition before it is claimable")
	return cmd
}

func newTaskReadyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ready <id>",
		Short: "Publish a draft task (draft -> ready)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.ReadyTask(cmd.Context(), args[0])
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
	return cmd
}

func newTaskReopenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reopen <id>",
		Short: "Reopen a delivered or abandoned task (merged|deployed_dev|deployed_prod|released|abandoned -> ready; a fresh claim is then required)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.ReopenTask(cmd.Context(), args[0])
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
	return cmd
}

func newTaskReworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rework <id>",
		Short: "Send a task under review back to in_progress (e.g. changes requested)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.ReworkTask(cmd.Context(), args[0])
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
	return cmd
}

// currentWorktreeIdentity derives the worktree identity for the current
// directory, used as the default lease binding for claim and claim --next.
func currentWorktreeIdentity() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	wt, err := worktree.Identity(cwd)
	if err != nil {
		return "", fmt.Errorf("not inside a git worktree; run from one or pass --worktree: %w", err)
	}
	return wt, nil
}

func newTaskClaimCmd() *cobra.Command {
	var worktree string
	var ttl time.Duration
	var next, strictFocus, dryRun bool
	var project string
	cmd := &cobra.Command{
		Use:   "claim [id]",
		Short: "Lease a task to the current worktree and move it to in_progress",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			project := resolveProject(cmd, project, cfg.CurrentProject)

			if !next {
				if len(args) == 0 {
					return fmt.Errorf("task id is required (or use --next)")
				}
				if worktree == "" {
					worktree, err = currentWorktreeIdentity()
					if err != nil {
						return err
					}
				}
				resp, raw, err := c.ClaimTask(cmd.Context(), args[0], worktree, ttl)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "claimed %s, lease expires %s\n", args[0], resp.Lease.ExpiresAt.Local().Format(time.RFC3339))
				fmt.Fprintf(out, "branch: %s\n\n", resp.Branch)
				fmt.Fprintf(out, "  git switch -c %s\n", resp.Branch)
				return nil
			}

			if len(args) > 0 {
				return fmt.Errorf("--next and a task id are mutually exclusive")
			}
			if worktree == "" && !dryRun {
				worktree, err = currentWorktreeIdentity()
				if err != nil {
					return err
				}
			}

			resp, raw, err := c.ClaimNext(cmd.Context(), cli.ClaimNextInput{
				Project: project, StrictFocus: strictFocus, DryRun: dryRun, Worktree: worktree, TTL: ttl,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			out := cmd.OutOrStdout()
			if resp.Task == nil {
				fmt.Fprintln(out, "no ready task")
				return nil
			}
			// The server is the authority on the branch name; the fallback
			// only covers a server too old to send one.
			branch := resp.Task.Branch
			if branch == "" {
				branch = store.DefaultBranchPrefix + resp.Task.ID + "-" + resp.Task.Slug
			}
			if resp.DryRun {
				fmt.Fprintf(out, "would claim %s (%s) — branch %s\n", resp.Task.ID, resp.Task.Slug, branch)
				return nil
			}
			fmt.Fprintf(out, "claimed %s (%s) — branch %s\n\n", resp.Task.ID, resp.Task.Slug, branch)
			fmt.Fprintf(out, "  git switch -c %s\n", branch)
			return nil
		},
	}
	cmd.Flags().StringVar(&worktree, "worktree", "", "worktree identity (default: <hostname>:<git worktree root> of the current directory)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "lease TTL (default 2h)")
	cmd.Flags().BoolVar(&next, "next", false, "claim the top-ranked ready task instead of a specific id (spec 02 ranking)")
	cmd.Flags().StringVar(&project, "project", "", "restrict --next to one project"+projectFlagUsage)
	cmd.Flags().BoolVar(&strictFocus, "strict-focus", false, "restrict --next to the project's focus concerns only")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --next, show the top-ranked candidate without claiming it")
	return cmd
}

func newTaskRenewCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "renew <id>",
		Short: "Extend the caller's lease on a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			l, raw, err := c.RenewLease(cmd.Context(), args[0], ttl)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "renewed %s, lease now expires %s\n", args[0], l.ExpiresAt.Local().Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "lease TTL (default 2h)")
	return cmd
}

func newTaskReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release <id>",
		Short: "Release the caller's lease on a task, returning it to ready",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.ReleaseLease(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "released %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func newTaskDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a task merged (in_review -> merged)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.DoneTask(cmd.Context(), args[0])
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
	return cmd
}

func newTaskAbandonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "abandon <id>",
		Short: "Abandon a task from any non-terminal state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.AbandonTask(cmd.Context(), args[0])
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
	return cmd
}

func newTaskBlockCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "block <id>",
		Short: "Record that another task blocks this one",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.Block(cmd.Context(), args[0], by)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now blocked by %s\n", args[0], by)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "id of the blocking task (required)")
	cmd.MarkFlagRequired("by")
	return cmd
}

func newTaskBriefCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brief <id>",
		Short: "Fetch a task's brief: body, branch, open blockers, and active lease",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			b, raw, err := c.Brief(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			printBrief(cmd, b)
			return nil
		},
	}
	return cmd
}

// printBrief renders a Brief as a readable summary, shared by `lode task
// brief` and `lode next`.
func printBrief(cmd *cobra.Command, b cli.Brief) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: %s\n", b.Task.ID, b.Task.Title)
	fmt.Fprintf(out, "state: %s   priority: %s\n", b.Task.State, b.Task.Priority)
	fmt.Fprintf(out, "branch: %s\n", b.Branch)
	if b.Lease != nil {
		fmt.Fprintf(out, "lease: %s (expires %s)\n", b.Lease.Worktree, b.Lease.ExpiresAt.Local().Format(time.RFC3339))
	}
	if len(b.OpenBlockers) > 0 {
		fmt.Fprintln(out, "blocked by:")
		for _, blk := range b.OpenBlockers {
			fmt.Fprintf(out, "  - %s: %s (%s)\n", blk.ID, blk.Title, blk.State)
		}
	}
	if b.Body != "" {
		fmt.Fprintf(out, "\n%s\n", b.Body)
	}
}

func newTaskUnblockCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "unblock <id>",
		Short: "Remove a blocking edge from another task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.Unblock(cmd.Context(), args[0], by)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer blocked by %s\n", args[0], by)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "id of the blocking task (required)")
	cmd.MarkFlagRequired("by")
	return cmd
}
