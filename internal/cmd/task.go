package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
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
		newTaskAssignCmd(),
		newTaskUnassignCmd(),
		newTaskStartCmd(),
		newTaskStopCmd(),
		newTaskSubmitCmd(),
		newTaskDoneCmd(),
		newTaskAbandonCmd(),
		newTaskBlockCmd(),
		newTaskUnblockCmd(),
		newTaskParentCmd(),
		newTaskUnparentCmd(),
		newTaskFollowUpCmd(),
		newTaskUnfollowUpCmd(),
		newTaskTreeCmd(),
		newTaskDecomposeCmd(),
		newTaskBriefCmd(),
		newTaskCostCmd(),
		newTaskSkillsCmd(),
	)
	return cmd
}

func init() {
	rootCmd.AddCommand(newTaskCmd())
}

// resolveBody returns the task body from --body / --body-file (spec 025 §18,
// the gh convention): bodyFile wins when set, with "-" reading stdin. Flag
// exclusivity is enforced by cobra (MarkFlagsMutuallyExclusive), not here.
func resolveBody(body, bodyFile string, stdin io.Reader) (string, error) {
	if bodyFile == "" {
		return body, nil
	}
	if bodyFile == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(bodyFile)
	if err != nil {
		return "", fmt.Errorf("read body file: %w", err)
	}
	return string(b), nil
}

func newTaskAddCmd() *cobra.Command {
	var scope scopeFlags
	var title, body, bodyFile, priority, kind, concern, parent, followUpTo string
	var draft bool
	var skills []string
	var secretNames []string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			warnDeprecatedKind(cmd, kind)
			body, err := resolveBody(body, bodyFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
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
			t, raw, err := c.CreateTask(cmd.Context(), model.CreateTaskInput{
				Project: sc.Project, Title: title, Body: body, Priority: priority, Kind: kind,
				Concern: concern, Draft: draft, Skills: skills, Parent: parent, FollowUpTo: followUpTo,
				Secrets: secretNames,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the task body from a file (\"-\" for stdin)")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.Flags().StringVar(&priority, "priority", "medium", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kind, "kind", "feature", "kind: feature, bug, chore, design, review, spike")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security (optional)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as draft (not claimable until published with `lode task ready`)")
	cmd.Flags().StringArrayVar(&skills, "skill", nil, "pin a skill name for recommendation (repeat the flag for each one; not comma-separated)")
	cmd.Flags().StringVar(&parent, "parent", "", "file the new task under this parent")
	cmd.Flags().StringVar(&followUpTo, "follow-up-to", "",
		"record that this task was spun out of the work on that task")
	cmd.Flags().StringSliceVar(&secretNames, "secrets", nil,
		"org-catalog secret names this task needs, comma-separated (see `lode secrets catalog`)")
	cmd.MarkFlagRequired("title")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var scope scopeFlags
	var priority, kind, parent, assignee, plan string
	var statuses []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (delivered and abandoned are hidden unless requested with --status)",
		RunE: func(cmd *cobra.Command, args []string) error {
			warnDeprecatedKind(cmd, kind)
			states := resolveStatusFilter(statuses)
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			var planDoc int64
			if plan != "" {
				if planDoc, err = resolveDocID(cmd.Context(), c, plan); err != nil {
					return err
				}
			}
			resp, raw, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
				Project: sc.Project, States: states, Priority: priority, Kind: kind, Parent: parent,
				Assignee: assignee, PlanDoc: planDoc,
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
	cmd.Flags().StringVar(&parent, "parent", "", "list only the direct children of this task")
	cmd.Flags().StringVar(&priority, "priority", "", "filter by priority")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: feature, bug, chore, design, review, spike")
	// No --mine: the CLI has no caller identity to resolve it to (see
	// docs/follow-ups.md).
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee actor id")
	cmd.Flags().StringVar(&plan, "plan", "", "list only the tasks minted by this plan document (id or slug, 025 §9.2)")
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
	var pager bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task's details: body, edges, blocked status, and lease holder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanupPager := withPager(cmd, pager)
			defer cleanupPager()
			return runTaskShow(cmd, args[0])
		},
	}
	cmd.Flags().BoolVarP(&pager, "pager", "p", false, pagerFlagUsage)
	return cmd
}

// runTaskShow is `task show`'s body, shared with the `lode show <id>`
// dispatcher (show.go) once it has classified arg as a task id.
func runTaskShow(cmd *cobra.Command, arg string) error {
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	id, err := resolveTaskID(cmd.Context(), arg, c, cfg)
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
}

func newTaskSkillsCmd() *cobra.Command {
	var set []string
	cmd := &cobra.Command{
		Use:   "skills <id>",
		Short: "Show or replace the task's pinned skills",
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
			if cmd.Flags().Changed("set") {
				raw, err := c.SetTaskSkills(cmd.Context(), id, set)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				var resp model.TaskSkills
				if err := json.Unmarshal(raw, &resp); err != nil {
					return fmt.Errorf("decode skills: %w", err)
				}
				printSkills(cmd, resp.Skills)
				return nil
			}
			t, raw, err := c.GetTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			printSkills(cmd, t.Skills)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&set, "set", nil, "replace pinned skills (comma-separated)")
	return cmd
}

// printSkills renders a task's pinned skills, one per line, or a note when
// there are none — a bare blank line reads as a rendering bug, not "no pins".
func printSkills(cmd *cobra.Command, skills []string) {
	if len(skills) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no pinned skills)")
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), strings.Join(skills, "\n"))
}

func newTaskEditCmd() *cobra.Command {
	var title, body, bodyFile, concern, priority string
	var needsDecomposition bool
	var secretNames []string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task's title, body, concern, priority, or needs-decomposition flag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var in model.EditTaskInput
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
			if cmd.Flags().Changed("secrets") {
				names := secretNames
				if len(names) == 1 && names[0] == "none" {
					names = []string{}
				}
				in.Secrets = &names
			}
			if in.Title == nil && in.Body == nil && in.Concern == nil && in.Priority == nil && in.NeedsDecomposition == nil && in.Secrets == nil {
				return fmt.Errorf("nothing to edit: set --title, --body, --body-file, --concern, --priority, --needs-decomposition, or --secrets")
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
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "replace the task title (must not be blank)")
	cmd.Flags().StringVar(&body, "body", "", "replace the task body with this text")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "replace the task body with the contents of this file (- for stdin)")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security, or none to clear")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: critical, high, medium, low")
	cmd.Flags().BoolVar(&needsDecomposition, "needs-decomposition", false, "mark (or unmark) the task as needing decomposition before it is claimable")
	cmd.Flags().StringSliceVar(&secretNames, "secrets", nil,
		"replace the task's declared secret names (comma-separated; 'none' clears)")
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
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
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
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
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
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
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
	var kind string
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
			warnDeprecatedKind(cmd, kind)
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

			in := model.ClaimNextInput{
				Project: sc.Project, Kind: kind, StrictFocus: strictFocus, DryRun: dryRun, Worktree: worktree,
			}
			if ttl > 0 {
				in.TTLSeconds = int(ttl.Seconds())
			}
			resp, raw, err := c.ClaimNext(cmd.Context(), in)
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
				branch = resp.Task.ID + "-" + resp.Task.Slug
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
	cmd.Flags().StringVar(&kind, "kind", "", "with --next, restrict the pick to a kind: feature, bug, chore, design, review, spike")
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
			clearTaskBindingIfCurrent(cmd, id)
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

func newTaskAssignCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "assign <id> [--to <actor>]",
		Short: "Assign a task to an actor (default: yourself)",
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
			t, raw, err := c.AssignTask(cmd.Context(), id, to)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "actor id to assign the task to (default: yourself)")
	return cmd
}

func newTaskUnassignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unassign <id>",
		Short: "Clear a task's assignee",
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
			t, raw, err := c.UnassignTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
			return nil
		},
	}
	return cmd
}

func newTaskStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <id>",
		Short: "Start working on a task you own (assigns you if unassigned). No worktree, no lease — for agent claims use `lode task claim`.",
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
			t, raw, err := c.StartTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
			return nil
		},
	}
	return cmd
}

func newTaskStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <id>",
		Short: "Put a started task back to ready; keeps the assignment.",
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
			t, raw, err := c.StopTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
			return nil
		},
	}
	return cmd
}

func newTaskSubmitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit <id>",
		Short: "Move your in-progress task to review.",
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
			t, raw, err := c.SubmitTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
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
			clearTaskBindingIfCurrent(cmd, id)
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
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
			clearTaskBindingIfCurrent(cmd, id)
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
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
func printBrief(cmd *cobra.Command, b model.Brief) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: %s\n", b.Task.ID, b.Task.Title)
	fmt.Fprintf(out, "state: %s   priority: %s\n", b.Task.State, b.Task.Priority)
	fmt.Fprintf(out, "branch: %s\n", b.Branch)
	if len(b.Task.Secrets) > 0 {
		fmt.Fprintf(out, "secrets: %s\n", strings.Join(b.Task.Secrets, ", "))
	}
	if b.Lease != nil {
		fmt.Fprintf(out, "lease: %s (expires %s)\n", b.Lease.Worktree, b.Lease.ExpiresAt.Local().Format(time.RFC3339))
	}
	if len(b.OpenBlockers) > 0 {
		fmt.Fprintln(out, "blocked by:")
		for _, blk := range b.OpenBlockers {
			fmt.Fprintf(out, "  - %s: %s (%s)\n", blk.ID, blk.Title, blk.State)
		}
	}
	if len(b.BlockingPlans) > 0 {
		fmt.Fprintln(out, "blocked by plans:")
		for _, p := range b.BlockingPlans {
			fmt.Fprintf(out, "  - %s: %s (%s)\n", p.Slug, p.Title, p.Status)
		}
	}
	if b.Body != "" {
		fmt.Fprintln(out)
		cli.Markdown(out, b.Body)
	}
	// Warnings alone still print the section: a user who misspelled every pin
	// would otherwise see nothing at all, which is exactly the case the
	// warnings exist for.
	if len(b.Skills.Pinned) > 0 || len(b.Skills.Matches) > 0 || len(b.Skills.Warnings) > 0 {
		fmt.Fprintln(out, "\nSkills:")
		for _, p := range b.Skills.Pinned {
			fmt.Fprintf(out, "  pinned  %s — %s (content in brief)\n", p.Name, p.Description)
		}
		for _, m := range b.Skills.Matches {
			fmt.Fprintf(out, "  %.2f    %s — %s\n", m.Score, m.Name, m.Description)
		}
		for _, w := range b.Skills.Warnings {
			fmt.Fprintf(out, "  warning: %s\n", w)
		}
	}
}

// newTaskCostCmd is `lode task cost <id>`: the tokens billed to a task (spec
// 025 §15.6, AC31). Unlike `lode project show`, --days defaults to 0 (all
// history) — a task's life is short, so there is no "recent window" to
// default to.
func newTaskCostCmd() *cobra.Command {
	var days int
	var children bool
	cmd := &cobra.Command{
		Use:   "cost <id>",
		Short: "Show the token cost billed to a task",
		Long: "Show the token cost billed to a task: the usage of agent sessions\n" +
			"that held a lease on the task. A container task reports its own\n" +
			"sessions unless --children is given, which folds in its child_of\n" +
			"descendants' sessions too.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			from, to := costWindow(days)
			tc, raw, err := c.TaskCost(cmd.Context(), id, children, from, to)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			printTaskCost(cmd, tc, costWindowLabel(days))
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 0, "cost window in days, counting today; 0 for all history")
	cmd.Flags().BoolVar(&children, "children", false, "include the task's child_of descendants' sessions")
	return cmd
}

// printTaskCost renders `lode task cost`: which task and scope, how many
// agent sessions billed usage, then the cost blocks printCost already knows
// how to render.
func printTaskCost(cmd *cobra.Command, tc model.TaskCost, window string) {
	out := cmd.OutOrStdout()
	if tc.IncludesChildren {
		fmt.Fprintf(out, "%s (including child tasks)\n", tc.Task)
	} else {
		fmt.Fprintf(out, "%s\n", tc.Task)
	}
	fmt.Fprintf(out, "sessions with recorded usage: %d\n", tc.Sessions)
	printCost(out, tc.Cost, window)
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
		Short: "File a task under a parent task",
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
	cmd.Flags().StringVar(&under, "under", "", "id of the parent to file it under (required)")
	cmd.MarkFlagRequired("under")
	return cmd
}

func newTaskUnparentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unparent <id>",
		Short: "Detach a task from its parent",
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
			parent := t.Hierarchy.Parent.ID
			raw, err := c.Unparent(cmd.Context(), id, parent)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer a child of %s\n", id, parent)
			return nil
		},
	}
	return cmd
}

func newTaskFollowUpCmd() *cobra.Command {
	var of string
	cmd := &cobra.Command{
		Use:   "follow-up <id>",
		Short: "Record that a task was spun out of the work on another task",
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
			origin, err := resolveTaskID(cmd.Context(), of, c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.FollowUp(cmd.Context(), id, origin)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now a follow-up to %s\n", id, origin)
			return nil
		},
	}
	cmd.Flags().StringVar(&of, "of", "", "id of the task this one was spun out of (required)")
	cmd.MarkFlagRequired("of")
	return cmd
}

func newTaskUnfollowUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unfollow-up <id>",
		Short: "Drop a task's follow-up edge to its origin",
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
			// The edge is identified by both endpoints and the caller knows
			// only one, so read the origin back first.
			t, _, err := c.GetTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			var origin string
			for _, e := range t.Edges.Out {
				if e.Type == "follow_up_to" {
					origin = e.To
					break
				}
			}
			if origin == "" {
				return fmt.Errorf("%s is not a follow-up to anything", id)
			}
			raw, err := c.UnfollowUp(cmd.Context(), id, origin)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer a follow-up to %s\n", id, origin)
			return nil
		},
	}
	return cmd
}

func newTaskTreeCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "tree [id]",
		Short: "Show tasks with children, and their children, with per-parent progress",
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

			// Each parent and its progress, before its children are fetched.
			type parentNode struct {
				task     model.Task
				progress model.TaskProgress
			}
			var parents []parentNode
			if len(args) == 1 {
				id, err := resolveTaskIDInScope(cmd.Context(), args[0], c, sc)
				if err != nil {
					return err
				}
				t, _, err := c.GetTask(cmd.Context(), id)
				if err != nil {
					return err
				}
				parents = []parentNode{{task: t.Task, progress: t.Hierarchy.Progress}}
			} else {
				// has_children selects containers — no kind declares one. The
				// roots are the ones with no parent of their own, so a
				// subtask's parent is not listed a second time.
				resp, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
					Project: sc.Project, HasChildren: true, States: resolveStatusFilter(nil),
				})
				if err != nil {
					return err
				}
				// One GetTask per parent, for the progress the list omits.
				for _, e := range resp.Tasks {
					detail, _, err := c.GetTask(cmd.Context(), e.ID)
					if err != nil {
						return err
					}
					if detail.Hierarchy.Parent != nil {
						continue
					}
					parents = append(parents, parentNode{task: e, progress: detail.Hierarchy.Progress})
				}
			}

			// One more round trip per parent, for its children.
			nodes := make([]cli.TreeNode, 0, len(parents))
			for _, e := range parents {
				kids, _, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{Parent: e.task.ID})
				if err != nil {
					return err
				}
				nodes = append(nodes, cli.TreeNode{
					Parent: e.task, Progress: e.progress, Children: kids.Tasks,
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
		Short: "Split an oversized task into children, in place",
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
			fmt.Fprintf(cmd.OutOrStdout(), "%s now has %d children\n",
				resp.Parent.ID, len(resp.Children))
			cli.TaskTable(cmd.OutOrStdout(), resp.Children)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&into, "into", nil,
		`child title (repeatable; one per child, e.g. --into "A" --into "B" --into "C")`)
	cmd.MarkFlagRequired("into")
	return cmd
}
