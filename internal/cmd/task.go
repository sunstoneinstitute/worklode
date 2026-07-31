package cmd

import (
	"errors"
	"fmt"
	"io"
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
		newTaskParentCmd(),
		newTaskUnparentCmd(),
		newTaskTreeCmd(),
		newTaskDecomposeCmd(),
		newTaskBriefCmd(),
	)
	return cmd
}

func init() {
	rootCmd.AddCommand(newTaskCmd())
}

func newTaskAddCmd() *cobra.Command {
	var scope scopeFlags
	var title, body, priority, kind, concern, parent string
	var draft bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errors.New(`no project: pass --project or --repo, set current_project in .worklode/config.toml or ~/.config/worklode/config.toml, or map this repo with "lode project add-repo"`)
			}
			t, raw, err := c.CreateTask(cmd.Context(), cli.CreateTaskInput{
				Project: sc.Project, Title: title, Body: body, Priority: priority, Kind: kind,
				Concern: concern, Draft: draft, Parent: parent,
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
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&priority, "priority", "medium", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kind, "kind", "feature", "kind: feature, bug, chore, spec, epic")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security (optional)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as draft (not claimable until published with `lode task ready`)")
	cmd.Flags().StringVar(&parent, "parent", "", "file the new task under this epic")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var scope scopeFlags
	var priority, parent string
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
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
				Project: sc.Project, States: states, Priority: priority, Parent: parent,
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
	addScopeFlags(cmd, &scope, "filter by project id")
	cmd.Flags().StringArrayVar(&statuses, "status", nil, "filter by status: draft, ready, in_progress, in_review, merged, deployed_dev, deployed_prod, released, abandoned, or all (repeatable; default hides merged, deployed_dev, deployed_prod, released, and abandoned)")
	cmd.Flags().StringVar(&parent, "parent", "", "list only the direct children of this epic")
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.GetTask(cmd.Context(), id)
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
	var title, body, bodyFile, concern, priority string
	var needsDecomposition bool
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task's title, body, concern, priority, or needs-decomposition flag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var in cli.EditTaskInput
			if cmd.Flags().Changed("title") {
				in.Title = &title
			}
			switch {
			case cmd.Flags().Changed("body") && cmd.Flags().Changed("body-file"):
				return fmt.Errorf("--body and --body-file are mutually exclusive")
			case cmd.Flags().Changed("body"):
				in.Body = &body
			case cmd.Flags().Changed("body-file"):
				text, err := readBodyFile(cmd, bodyFile)
				if err != nil {
					return err
				}
				in.Body = &text
			}
			if cmd.Flags().Changed("concern") {
				in.Concern = &concern
			}
			if cmd.Flags().Changed("priority") {
				in.Priority = &priority
			}
			if cmd.Flags().Changed("needs-decomposition") {
				in.NeedsDecomposition = &needsDecomposition
			}
			if in.Title == nil && in.Body == nil && in.Concern == nil && in.Priority == nil && in.NeedsDecomposition == nil {
				return fmt.Errorf("nothing to edit: set --title, --body, --body-file, --concern, --priority, or --needs-decomposition")
			}

			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.EditTask(cmd.Context(), id, in)
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
	cmd.Flags().StringVar(&title, "title", "", "replace the task title (must not be blank)")
	cmd.Flags().StringVar(&body, "body", "", "replace the task body with this text")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "replace the task body with the contents of this file (- for stdin)")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security, or none to clear")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: critical, high, medium, low")
	cmd.Flags().BoolVar(&needsDecomposition, "needs-decomposition", false, "mark (or unmark) the task as needing decomposition before it is claimable")
	return cmd
}

// readBodyFile reads a task body from path, or from the command's stdin when
// path is "-". Multi-line markdown bodies are awkward to pass as a flag value,
// so `lode task edit --body-file -` is the pipe-friendly form.
func readBodyFile(cmd *cobra.Command, path string) (string, error) {
	if path == "-" {
		text, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
		return string(text), nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(text), nil
}

func newTaskReadyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ready <id>",
		Short: "Publish a draft task (draft -> ready)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.ReadyTask(cmd.Context(), id)
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.ReopenTask(cmd.Context(), id)
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.ReworkTask(cmd.Context(), id)
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
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "claim [id]",
		Short: "Lease a task to the current worktree and move it to in_progress",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}

			if !next {
				if len(args) == 0 {
					return fmt.Errorf("task id is required (or use --next)")
				}
				sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
				if err != nil {
					return err
				}
				id, err := resolveTaskIDInScope(cmd.Context(), args[0], c, sc)
				if err != nil {
					return err
				}
				if worktree == "" {
					worktree, err = currentWorktreeIdentity()
					if err != nil {
						return err
					}
				}
				resp, raw, err := c.ClaimTask(cmd.Context(), id, worktree, ttl)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "claimed %s, lease expires %s\n", id, resp.Lease.ExpiresAt.Local().Format(time.RFC3339))
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
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}

			resp, raw, err := c.ClaimNext(cmd.Context(), cli.ClaimNextInput{
				Project: sc.Project, StrictFocus: strictFocus, DryRun: dryRun, Worktree: worktree, TTL: ttl,
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
	cmd.Flags().BoolVar(&next, "next", false, "claim the top-ranked ready task instead of a specific id (spec 005 ranking)")
	addScopeFlags(cmd, &scope, "the project a bare task number belongs to; with --next, restrict the pick to a project")
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			l, raw, err := c.RenewLease(cmd.Context(), id, ttl)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "renewed %s, lease now expires %s\n", id, l.ExpiresAt.Local().Format(time.RFC3339))
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.ReleaseLease(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "released %s\n", id)
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.DoneTask(cmd.Context(), id)
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.AbandonTask(cmd.Context(), id)
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			by, err = resolveTaskID(cmd.Context(), by, c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.Block(cmd.Context(), id, by)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now blocked by %s\n", id, by)
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
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			b, raw, err := c.Brief(cmd.Context(), id)
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
		fmt.Fprintln(out)
		cli.Markdown(out, b.Body)
	}
}

func newTaskUnblockCmd() *cobra.Command {
	var by string
	cmd := &cobra.Command{
		Use:   "unblock <id>",
		Short: "Remove a blocking edge from another task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			by, err = resolveTaskID(cmd.Context(), by, c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.Unblock(cmd.Context(), id, by)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer blocked by %s\n", id, by)
			return nil
		},
	}
	cmd.Flags().StringVar(&by, "by", "", "id of the blocking task (required)")
	cmd.MarkFlagRequired("by")
	return cmd
}

func newTaskParentCmd() *cobra.Command {
	var under string
	cmd := &cobra.Command{
		Use:   "parent <id>",
		Short: "File a task under an epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			under, err = resolveTaskID(cmd.Context(), under, c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.Parent(cmd.Context(), id, under)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now a child of %s\n", id, under)
			return nil
		},
	}
	cmd.Flags().StringVar(&under, "under", "", "id of the epic to file it under (required)")
	cmd.MarkFlagRequired("under")
	return cmd
}

func newTaskUnparentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unparent <id>",
		Short: "Detach a task from its epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			// The edge is identified by both endpoints, and the caller only
			// knows the child, so read the parent back first.
			t, _, err := c.GetTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			if t.Hierarchy.Parent == nil {
				return fmt.Errorf("%s has no parent", id)
			}
			epic := t.Hierarchy.Parent.ID
			raw, err := c.Unparent(cmd.Context(), id, epic)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer a child of %s\n", id, epic)
			return nil
		},
	}
	return cmd
}

func newTaskTreeCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "tree [id]",
		Short: "Show epics and their children, with per-epic progress",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}

			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}

			// Each epic and its progress, before its children are fetched.
			type epicNode struct {
				task     cli.Task
				progress cli.TaskProgress
			}
			var epics []epicNode
			if len(args) == 1 {
				id, err := resolveTaskIDInScope(cmd.Context(), args[0], c, sc)
				if err != nil {
					return err
				}
				t, _, err := c.GetTask(cmd.Context(), id)
				if err != nil {
					return err
				}
				epics = []epicNode{{task: t.Task, progress: t.Hierarchy.Progress}}
			} else {
				resp, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
					Project: sc.Project, Kind: "epic", States: resolveStatusFilter(nil),
				})
				if err != nil {
					return err
				}
				// One GetTask per epic, for the progress the list omits.
				for _, e := range resp.Tasks {
					detail, _, err := c.GetTask(cmd.Context(), e.ID)
					if err != nil {
						return err
					}
					epics = append(epics, epicNode{task: e, progress: detail.Hierarchy.Progress})
				}
			}

			// One more round trip per epic, for its children.
			nodes := make([]cli.TreeNode, 0, len(epics))
			for _, e := range epics {
				kids, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{Parent: e.task.ID})
				if err != nil {
					return err
				}
				nodes = append(nodes, cli.TreeNode{
					Epic: e.task, Progress: e.progress, Children: kids.Tasks,
				})
			}
			cli.TreeRender(cmd.OutOrStdout(), nodes)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	return cmd
}

func newTaskDecomposeCmd() *cobra.Command {
	var into []string
	cmd := &cobra.Command{
		Use:   "decompose <id>",
		Short: "Turn an oversized task into an epic plus its children, in place",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			resp, raw, err := c.Decompose(cmd.Context(), id, into)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now an epic with %d children\n",
				resp.Epic.ID, len(resp.Children))
			cli.TaskTable(cmd.OutOrStdout(), resp.Children)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&into, "into", nil,
		`child title (repeatable; one per child, e.g. --into "A" --into "B" --into "C")`)
	cmd.MarkFlagRequired("into")
	return cmd
}
