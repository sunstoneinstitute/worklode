package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
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
		newTaskReadyCmd(),
		newTaskReopenCmd(),
		newTaskClaimCmd(),
		newTaskRenewCmd(),
		newTaskReleaseCmd(),
		newTaskDoneCmd(),
		newTaskAbandonCmd(),
		newTaskBlockCmd(),
		newTaskUnblockCmd(),
	)
	return cmd
}

func init() {
	rootCmd.AddCommand(newTaskCmd())
}

func newTaskAddCmd() *cobra.Command {
	var project, title, body, priority, kind string
	var draft bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			t, raw, err := c.CreateTask(cmd.Context(), cli.CreateTaskInput{
				Project: project, Title: title, Body: body, Priority: priority, Kind: kind, Draft: draft,
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
	cmd.Flags().StringVar(&project, "project", "", "project id (required)")
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&priority, "priority", "medium", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kind, "kind", "feature", "kind: feature, bug, chore, spec")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as draft (not claimable until published with `lode task ready`)")
	cmd.MarkFlagRequired("project")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var project, priority string
	var states []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
				Project: project, States: states, Priority: priority,
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
	cmd.Flags().StringVar(&project, "project", "", "filter by project id")
	cmd.Flags().StringArrayVar(&states, "state", nil, "filter by state (repeatable)")
	cmd.Flags().StringVar(&priority, "priority", "", "filter by priority")
	return cmd
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
		Short: "Reopen a task under review (in_review -> in_progress, e.g. changes requested)",
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

func newTaskClaimCmd() *cobra.Command {
	var worktree string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "claim <id>",
		Short: "Lease a task to the current worktree and move it to in_progress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			if worktree == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("determine working directory: %w", err)
				}
				worktree, err = cli.WorktreeIdentity(cwd)
				if err != nil {
					return fmt.Errorf("not inside a git worktree; run from one or pass --worktree: %w", err)
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
		},
	}
	cmd.Flags().StringVar(&worktree, "worktree", "", "worktree identity (default: <hostname>:<git worktree root> of the current directory)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "lease TTL (default 2h)")
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
		Short: "Mark a task done (in_review -> done)",
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
