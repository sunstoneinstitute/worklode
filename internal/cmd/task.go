package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/blobref"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/ns"
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
		newTaskPublishCmd(),
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
		newTaskSetCmd(),
		newTaskAbandonCmd(),
		newTaskDeleteCmd(),
		newTaskUndeleteCmd(),
		newTaskBlockCmd(),
		newTaskBlockersCmd(),
		newTaskUnblockCmd(),
		newTaskParentCmd(),
		newTaskUnparentCmd(),
		newTaskFollowUpCmd(),
		newTaskUnfollowUpCmd(),
		newTaskDuplicateCmd(),
		newTaskUnduplicateCmd(),
		newTaskTreeCmd(),
		newTaskDecomposeCmd(),
		newBoardCmd(),
		newTaskBriefCmd(),
		newTaskFrontierCmd(),
		newTaskCostCmd(),
		newCriticalPathCmd(),
		newTimelineCmd(),
		newTaskSkillsCmd(),
		newTaskChecklistCmd(),
		newTaskAttachCmd(),
		newTaskDetachCmd(),
		newTaskInstructCmd(),
		newReconcileCmd(),
	)
	return cmd
}

func init() {
	rootCmd.AddCommand(newTaskCmd())
}

// taskTransition is the shape of every cli.Client call that moves one task by
// id; call sites pass a method expression, e.g. (*cli.Client).ReadyTask.
type taskTransition func(*cli.Client, context.Context, string) (model.Task, []byte, error)

// newTaskTransitionCmd builds a `lode task <verb> <id>` command that runs one
// transition and prints the task it returns. clearBinding drops the current
// worktree's task stamp when the transition ends the work on that task.
func newTaskTransitionCmd(use, short string, clearBinding bool, call taskTransition) *cobra.Command {
	return &cobra.Command{
		Use:               use,
		Short:             short,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := call(c, cmd.Context(), id)
			if err != nil {
				return err
			}
			if clearBinding {
				clearTaskBindingIfCurrent(cmd, id)
			}
			return renderTask(cmd, t, raw)
		},
	}
}

// renderTask prints one task as the package's standard single-row table, or
// the server's raw response under --json.
func renderTask(cmd *cobra.Command, t model.Task, raw []byte) error {
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	cli.TaskTable(cmd.OutOrStdout(), []model.Task{t})
	return nil
}

// taskEdge is the shape of every cli.Client call that adds or removes an edge
// between two tasks.
type taskEdge func(*cli.Client, context.Context, string, string) ([]byte, error)

// newTaskEdgeCmd builds a `lode task <verb> <id> --<flag> <other-id>` command.
// msg is the confirmation line, formatted with the subject id and the other
// endpoint in that order.
func newTaskEdgeCmd(use, short, flag, flagHelp, msg string, call taskEdge) *cobra.Command {
	var other string
	cmd := &cobra.Command{
		Use:               use,
		Short:             short,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, otherID, err := resolveTaskIDPair(cmd.Context(), args[0], other, c, cfg)
			if err != nil {
				return err
			}
			return runTaskEdge(cmd, c, id, otherID, msg, call)
		},
	}
	cmd.Flags().StringVar(&other, flag, "", flagHelp)
	cmd.MarkFlagRequired(flag)
	return cmd
}

// newTaskUnEdgeCmd builds a `lode task <verb> <id>` command that drops an edge
// the caller named only one end of: find reads the other endpoint off the
// task, and reports why there is nothing to drop when there is none.
func newTaskUnEdgeCmd(use, short, msg string, find func(model.TaskDetail) (string, error), call taskEdge) *cobra.Command {
	return &cobra.Command{
		Use:               use,
		Short:             short,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			// only one, so read the other back first.
			t, _, err := c.GetTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			otherID, err := find(t)
			if err != nil {
				return err
			}
			return runTaskEdge(cmd, c, id, otherID, msg, call)
		},
	}
}

// runTaskEdge issues one edge call and prints its confirmation line.
func runTaskEdge(cmd *cobra.Command, c *cli.Client, id, otherID, msg string, call taskEdge) error {
	raw, err := call(c, cmd.Context(), id, otherID)
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), msg+"\n", id, otherID)
	return nil
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

// uploadBodyImages uploads every local relative image the body references and
// returns the body with those destinations rewritten to /blob/<hash>.
//
// Uploads complete before the create/update call, so the task is written once
// with final content and the server's embedded reconciliation sees the
// rewritten body. A missing file fails the whole command rather than
// producing a task whose body points at images that were never uploaded.
func uploadBodyImages(ctx context.Context, c *cli.Client, body, baseDir string, out io.Writer) (string, error) {
	locals := blobref.LocalImages(body)
	if len(locals) == 0 {
		return body, nil
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	// The containment check has to compare resolved paths, because os.Open
	// follows symlinks: a bundle carrying `shot.png -> /etc/shadow` would
	// otherwise pass a purely lexical test and upload that file's bytes.
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", baseDir, err)
	}
	mapping := make(map[string]string, len(locals))
	for _, rel := range locals {
		// A markdown destination is URL-escaped, the filesystem name is not,
		// so `./my%20file.png` has to be decoded before it is opened. The raw
		// destination stays the rewrite key: it is the string actually
		// written in the body, and the two differ. Decoding belongs here and
		// not in blobref, which only ever deals in body text.
		name := rel
		if dec, err := url.PathUnescape(rel); err == nil {
			name = dec
		}
		abs := filepath.Join(base, name)
		// Lstat before EvalSymlinks so a genuinely missing file still
		// reports "no such file" rather than a confusing resolve error.
		if _, err := os.Lstat(abs); err != nil {
			return "", fmt.Errorf("image %q: %w", rel, err)
		}
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil {
			return "", fmt.Errorf("image %q: %w", rel, err)
		}
		if !withinDir(base, abs) {
			return "", fmt.Errorf("image %q resolves outside %s", rel, base)
		}
		blob, err := c.UploadFile(ctx, abs)
		if err != nil {
			return "", fmt.Errorf("upload %q: %w", rel, err)
		}
		mapping[rel] = blob.URL
		fmt.Fprintf(out, "uploaded %s (%s, %d bytes)\n", rel, blob.MediaType, blob.Size)
	}
	return blobref.ReplaceDestination(body, mapping)
}

// withinDir reports whether path is dir or below it. filepath.Rel rather than
// a string prefix, which would accept /foo/bar for a /foo/ba base. Both
// arguments must already be absolute and symlink-resolved.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func newTaskAddCmd() *cobra.Command {
	var scope scopeFlags
	var title, body, bodyFile, priority, kind, concern, parent, followUpTo string
	var draft, noUpload bool
	var skills []string
	var secretNames []string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			warnDeprecatedTaskKind(cmd, kind)
			body, err := resolveBody(body, bodyFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			if bodyFile != "" && bodyFile != "-" && !noUpload {
				body, err = uploadBodyImages(cmd.Context(), c, body,
					filepath.Dir(bodyFile), cmd.OutOrStdout())
				if err != nil {
					return err
				}
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if sc.Project == "" {
				return errNoProject
			}
			t, raw, err := c.CreateTask(cmd.Context(), model.CreateTaskInput{
				Project: sc.Project, Title: title, Body: body, Priority: priority, Kind: kind,
				Concern: concern, Draft: draft, Skills: skills, Parent: parent, FollowUpTo: followUpTo,
				Secrets: secretNames,
			})
			if err != nil {
				return err
			}
			return renderTask(cmd, t, raw)
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	cmd.Flags().StringVar(&title, "title", "", "task title (required)")
	cmd.Flags().StringVar(&body, "body", "", "task body")
	cmd.Flags().StringVar(&bodyFile, "body-file", "",
		"read the body from this file (- for stdin); local images referenced from a file are uploaded and rewritten")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
	cmd.Flags().BoolVar(&noUpload, "no-upload", false, "do not upload local images referenced by --body-file")
	cmd.Flags().StringVar(&priority, "priority", "medium", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kind, "kind", "feature", "kind: feature, bug, chore, design, review, spike, decision")
	completeFlagValues(cmd, "priority", taskPriorities)
	completeFlagValues(cmd, "kind", ns.TaskKinds)
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security (optional)")
	cmd.Flags().BoolVar(&draft, "draft", false, "create as draft (not claimable until published with `lode task publish`)")
	cmd.Flags().StringArrayVar(&skills, "skill", nil, "pin a skill name for recommendation (repeat the flag for each one; not comma-separated)")
	cmd.Flags().StringVar(&parent, "parent", "", "file the new task under this parent")
	cmd.Flags().StringVar(&followUpTo, "follow-up-to", "",
		"record that this task was spun out of the work on that task")
	cmd.Flags().StringSliceVar(&secretNames, "secrets", nil,
		"org-catalog secret names this task needs, comma-separated (see `lode secret catalog`)")
	cmd.MarkFlagRequired("title")
	return cmd
}

// taskPriorities and taskStatusValues are the closed sets behind
// `lode task` --priority and --status. Neither has a Go declaration the CLI
// can reach — the priorities are internal/api's validPriorities and the
// states are the tasks.state CHECK constraint of migration 0005 — so they are
// mirrored here, beside the flags they complete. "all" is resolveStatusFilter's
// pseudo-status, not a state.
var (
	taskPriorities   = []string{"critical", "high", "medium", "low"}
	taskStatusValues = []string{
		"draft", "ready", "in_progress", "in_review",
		"merged", "deployed_dev", "deployed_prod", "released", "abandoned", "all",
	}
)

func newTaskListCmd() *cobra.Command {
	var scope scopeFlags
	var priority, kind, parent, assignee, plan, about string
	var statuses []string
	var deleted bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (delivered and abandoned are hidden unless requested with --status)",
		RunE: func(cmd *cobra.Command, args []string) error {
			warnDeprecatedTaskKind(cmd, kind)
			states := resolveDeletedStatusFilter(statuses, deleted, cmd.Flags().Changed("status"))
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			var planDoc, aboutDoc int64
			if plan != "" {
				if planDoc, err = resolveDocID(cmd.Context(), c, plan); err != nil {
					return err
				}
			}
			if about != "" {
				if aboutDoc, err = resolveDocID(cmd.Context(), c, about); err != nil {
					return err
				}
			}
			resp, raw, err := c.ListTasks(cmd.Context(), cli.TaskListFilter{
				Project: sc.Project, States: states, Priority: priority, Kind: kind, Parent: parent,
				Assignee: assignee, PlanDoc: planDoc, AboutDoc: aboutDoc, Deleted: deleted,
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
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: feature, bug, chore, design, review, spike, decision")
	completeFlagValues(cmd, "status", taskStatusValues)
	completeFlagValues(cmd, "priority", taskPriorities)
	completeFlagValues(cmd, "kind", ns.TaskKinds)
	// No --mine: the CLI has no caller identity to resolve it to (see
	// docs/follow-ups.md).
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee actor id")
	cmd.Flags().StringVar(&plan, "plan", "", "list only the tasks minted by this plan document (id or slug, 025 §9.2)")
	cmd.Flags().StringVar(&about, "about", "", "list only the tasks about this document — its review and planning tasks (id or slug, 025 §15.4)")
	cmd.Flags().BoolVar(&deleted, "deleted", false,
		"list deleted tasks instead of live ones, in any state unless --status is also given")
	return cmd
}

// resolveDeletedStatusFilter is resolveStatusFilter with `--deleted` folded
// in. A tombstone is orthogonal to state (044 §1), so a deleted task keeps
// whatever state it had — and the default open-state filter would hide most
// tombstones, which is the opposite of what someone asking for the deleted
// list wants. So `--deleted` alone drops the state filter entirely; an
// explicit `--status` still narrows within the tombstoned set.
func resolveDeletedStatusFilter(statuses []string, deleted, statusGiven bool) []string {
	if deleted && !statusGiven {
		return nil
	}
	return resolveStatusFilter(statuses)
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
		for _, part := range splitNames(s) {
			if part == "all" {
				return nil
			}
			states = append(states, part)
		}
	}
	return states
}

func newTaskShowCmd() *cobra.Command {
	var pager, usage bool
	cmd := &cobra.Command{
		Use:               "show <id>",
		Short:             "Show a task's details: body, edges, blocked status, and lease holder",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanupPager := withPager(cmd, pager)
			defer cleanupPager()
			return runTaskShow(cmd, args[0], usage)
		},
	}
	cmd.Flags().BoolVarP(&pager, "pager", "p", false, pagerFlagUsage)
	cmd.Flags().BoolVar(&usage, "usage", false, "include the task's token usage/cost (all history, own sessions only; see task cost for a window or --children)")
	return cmd
}

// runTaskShow is `task show`'s body, shared with the `lode show <id>`
// dispatcher (show.go) once it has classified arg as a task id. usage, when
// set, folds in the same all-history, own-sessions-only cost `task cost`
// reports by default (no --days/--children window here; use `task cost` for
// that).
func runTaskShow(cmd *cobra.Command, arg string, usage bool) error {
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
	if !usage {
		if jsonOut(cmd) {
			printRaw(cmd, raw)
			return nil
		}
		cli.TaskDetailRender(cmd.OutOrStdout(), t, cfg.ServerURL)
		return nil
	}
	tc, costRaw, err := c.TaskCost(cmd.Context(), id, false, time.Time{}, time.Time{})
	if err != nil {
		return err
	}
	if jsonOut(cmd) {
		return printJSON(cmd, taskShowUsageResult{
			Task: json.RawMessage(raw),
			Cost: json.RawMessage(costRaw),
		})
	}
	cli.TaskDetailRender(cmd.OutOrStdout(), t, cfg.ServerURL)
	fmt.Fprintln(cmd.OutOrStdout())
	cli.TaskCostRender(cmd.OutOrStdout(), tc, "all time")
	return nil
}

// taskShowUsageResult is `task show --usage`'s --json output: it splices two
// already-serialized API responses (the task and its cost) together for
// display. It never crosses the HTTP boundary itself, so it stays local to
// the CLI rather than living in internal/model (ADR 036 §2).
type taskShowUsageResult struct {
	Task json.RawMessage `json:"task"`
	Cost json.RawMessage `json:"cost"`
}

// newTaskSkillsCmd is the read-only view of a task's pinned skills (061 §2.2,
// rule L6). The paired write is `lode task set skills <name…> <id>`.
func newTaskSkillsCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "skills <id>",
		Short:             "Show the task's pinned skills",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			cli.PinnedSkillList(cmd.OutOrStdout(), t.Skills)
			return nil
		},
	}
}

func newTaskEditCmd() *cobra.Command {
	var title, body, bodyFile, concern, priority, kindFlag string
	var needsDecomposition, humanOnly, noUpload bool
	var secretNames, artifacts []string
	cmd := &cobra.Command{
		Use:               "edit <id>",
		Short:             "Edit a task's title, body, concern, priority, needs-decomposition or human-only flag, or declare an artifact it is verified by",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			if cmd.Flags().Changed("kind") {
				warnDeprecatedTaskKind(cmd, kindFlag)
				in.Kind = &kindFlag
			}
			if cmd.Flags().Changed("needs-decomposition") {
				in.NeedsDecomposition = &needsDecomposition
			}
			if cmd.Flags().Changed("human-only") {
				in.HumanOnly = &humanOnly
			}
			if cmd.Flags().Changed("secrets") {
				names := secretNames
				if len(names) == 1 && names[0] == "none" {
					names = []string{}
				}
				in.Secrets = &names
			}
			if cmd.Flags().Changed("artifact") {
				in.Artifacts = &artifacts
			}
			if in.Title == nil && in.Body == nil && in.Concern == nil && in.Priority == nil && in.NeedsDecomposition == nil && in.HumanOnly == nil && in.Secrets == nil && in.Artifacts == nil && in.Kind == nil {
				return fmt.Errorf("nothing to edit: set --title, --body, --body-file, --concern, --priority, --kind, --needs-decomposition, --human-only, --secrets, or --artifact")
			}

			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			// bodyFile != "-" means the body came from a real file with a
			// base directory to resolve local images against; --body and
			// --body-file - (stdin) never rewrite (no base directory).
			if in.Body != nil && cmd.Flags().Changed("body-file") && bodyFile != "-" && !noUpload {
				rewritten, err := uploadBodyImages(cmd.Context(), c, *in.Body,
					filepath.Dir(bodyFile), cmd.OutOrStdout())
				if err != nil {
					return err
				}
				in.Body = &rewritten
			}
			t, raw, err := c.EditTask(cmd.Context(), id, in)
			if err != nil {
				return err
			}
			return renderTask(cmd, t, raw)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "replace the task title (must not be blank)")
	cmd.Flags().StringVar(&body, "body", "", "replace the task body with this text")
	cmd.Flags().StringVar(&bodyFile, "body-file", "",
		"replace the task body with the contents of this file (- for stdin); local images referenced from a file are uploaded and rewritten")
	cmd.Flags().BoolVar(&noUpload, "no-upload", false, "do not upload local images referenced by --body-file")
	cmd.Flags().StringVar(&concern, "concern", "", "concern: completeness, performance, usability, security, or none to clear")
	cmd.Flags().StringVar(&priority, "priority", "", "priority: critical, high, medium, low")
	cmd.Flags().StringVar(&kindFlag, "kind", "", "retag the task's kind: feature, bug, chore, design, review, spike, decision")
	completeFlagValues(cmd, "priority", taskPriorities)
	completeFlagValues(cmd, "kind", ns.TaskKinds)
	cmd.Flags().BoolVar(&needsDecomposition, "needs-decomposition", false, "mark (or unmark) the task as needing decomposition before it is claimable")
	cmd.Flags().BoolVar(&humanOnly, "human-only", false, "mark (or unmark) the task as human-only: never offered by lode next or the frontier, still claimable by id")
	cmd.Flags().StringSliceVar(&secretNames, "secrets", nil,
		"replace the task's declared secret names (comma-separated; 'none' clears)")
	cmd.Flags().StringArrayVar(&artifacts, "artifact", nil,
		"declare a catalog address this task is verified by (repeat the flag for each; additive, never removes)")
	return cmd
}

// readBodyFile reads a body from path, or from the command's stdin when path
// is "-". Multi-line markdown bodies are awkward to pass as a flag value, so
// `lode task edit --body-file -` is the pipe-friendly form.
//
// An empty path is rejected rather than delegated: resolveBody reads "no file
// named" as "use the inline body", which for these callers — who have no
// inline body — would quietly overwrite a document or task body with nothing.
// MarkFlagRequired only checks that the flag was set, so `--file ""` reaches
// here.
func readBodyFile(cmd *cobra.Command, path string) (string, error) {
	if path == "" {
		return "", errors.New(`no file: pass a path, or "-" to read the body from stdin`)
	}
	return resolveBody("", path, cmd.InOrStdin())
}

func newTaskPublishCmd() *cobra.Command {
	return newTaskTransitionCmd("publish <id>", "Publish a draft task (draft -> ready)",
		false, (*cli.Client).ReadyTask)
}

func newTaskReopenCmd() *cobra.Command {
	return newTaskTransitionCmd("reopen <id>", "Reopen a delivered or abandoned task (merged|deployed_dev|deployed_prod|released|abandoned -> ready; a fresh claim is then required)",
		false, (*cli.Client).ReopenTask)
}

func newTaskReworkCmd() *cobra.Command {
	return newTaskTransitionCmd("rework <id>", "Send a task under review back to in_progress (e.g. changes requested)",
		false, (*cli.Client).ReworkTask)
}

// currentWorktreeIdentity derives the worktree identity for the current
// directory, used as the default lease binding for claim and claim --next.
//
// It refuses the main checkout: `task claim` binds a lease to whatever
// directory it runs from without creating one, so claiming from the main
// checkout leases it directly, a lease `lode work resume`/`lode work
// status` can never resolve back to a task worktree and a second claim from
// the same place then collides with (WL-383). Only `lode work next` is
// meant to enter Worklode from the main checkout — it creates the worktree first. IsMain's ok=false
// (can't tell) is treated as permission, not refusal: this check is a new
// guard against a real trap, not a reason to break claims in a repo layout
// it cannot read.
func currentWorktreeIdentity() (string, error) {
	cwd, err := workingDir()
	if err != nil {
		return "", err
	}
	root, ok := worktree.Root(cwd)
	if !ok {
		return "", fmt.Errorf("not inside a git worktree; run from one or pass --worktree")
	}
	if isMain, ok := worktree.IsMain(root); ok && isMain {
		return "", fmt.Errorf("%s is the main checkout, not a task worktree: claiming here binds "+
			"the lease to a directory `lode work resume`/`lode work status` "+
			"can't see. Create a worktree first (`lode work next [id]`, or "+
			"`git worktree add`) and run `lode task claim` from inside "+
			"it, or pass --worktree", root)
	}
	return worktree.IdentityOf(root)
}

func newTaskClaimCmd() *cobra.Command {
	var worktree string
	var ttl time.Duration
	var next, strictFocus, dryRun bool
	var kind string
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:               "claim [id]",
		Short:             "Lease a task to the current worktree and move it to in_progress",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
				fmt.Fprintf(out, "claimed %s, lease expires %s\n", id, cli.LocalTime(resp.Lease.ExpiresAt))
				fmt.Fprintf(out, "branch: %s\n\n", resp.Branch)
				fmt.Fprintf(out, "  git switch -c %s\n", resp.Branch)
				return nil
			}

			if len(args) > 0 {
				return fmt.Errorf("--next and a task id are mutually exclusive")
			}
			warnDeprecatedTaskKind(cmd, kind)
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
	cmd.Flags().StringVar(&kind, "kind", "", "with --next, restrict the pick to a kind: feature, bug, chore, design, review, spike, decision")
	completeFlagValues(cmd, "kind", ns.TaskKinds)
	cmd.Flags().BoolVar(&strictFocus, "strict-focus", false, "restrict --next to the project's focus concerns only")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --next, show the top-ranked candidate without claiming it")
	return cmd
}

func newTaskRenewCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:               "renew <id>",
		Short:             "Extend the caller's lease on a task",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			fmt.Fprintf(cmd.OutOrStdout(), "renewed %s, lease now expires %s\n", id, cli.LocalTime(l.ExpiresAt))
			return nil
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "lease TTL (default 2h)")
	return cmd
}

func newTaskReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "release <id>",
		Short:             "Release the caller's lease on a task, returning it to ready",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
		Use:               "assign <id> [--to <actor>]",
		Short:             "Assign a task to an actor (default: yourself)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			return renderTask(cmd, t, raw)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "actor id to assign the task to (default: yourself)")
	return cmd
}

func newTaskUnassignCmd() *cobra.Command {
	return newTaskTransitionCmd("unassign <id>", "Clear a task's assignee",
		false, (*cli.Client).UnassignTask)
}

func newTaskStartCmd() *cobra.Command {
	return newTaskTransitionCmd("start <id>", "Start working on a task you own (assigns you if unassigned). No worktree, no lease — for agent claims use `lode task claim`.",
		false, (*cli.Client).StartTask)
}

func newTaskStopCmd() *cobra.Command {
	return newTaskTransitionCmd("stop <id>", "Put a started task back to ready; keeps the assignment.",
		false, (*cli.Client).StopTask)
}

func newTaskSubmitCmd() *cobra.Command {
	return newTaskTransitionCmd("submit <id>", "Move your in-progress task to review.",
		false, (*cli.Client).SubmitTask)
}

// taskSetFields are the fields `lode task set` writes. The switch below
// handles each one and this list names them, for the unknown-field error and
// for completion (061 §1 L4): the field is an argument, so it completes.
var taskSetFields = []string{"state", "skills", "checklist"}

// newTaskSetCmd is `lode task set <field> <value…> <id>` (061 §2.1): write one
// named field on a task. The field and the values are arguments, not part of
// the verb, so this does not fit newTaskTransitionCmd.
//
// The task id is always the LAST argument, which is what lets a field take
// more than one value: "state" takes exactly one (the four delivery states an
// ingestion path normally supplies, reachable by hand for work no webhook can
// see), "skills" takes any number, and no names at all clears the pins.
//
// The field and a state value are checked here, before any client call, so a
// typo names the valid values instead of costing a round trip. Whether the
// named state is legal from where the task currently stands is not checked
// here at all: that is the server's transition table, and its refusal is
// returned unchanged.
func newTaskSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <field> <value…> <id>",
		Short: "Set one field on a task, e.g. `lode task set state merged WL-5`",
		Long: `Set one named field on a task. The task id is always the last argument;
everything between the field and the id is the value.

  state      exactly one of the delivery states:
               lode task set state merged WL-5
  skills     any number of skill names, replacing whatever was pinned:
               lode task set skills tdd debugging WL-5
             naming none clears the task's pinned skills:
               lode task set skills WL-5
  checklist  an item (ordinal, canonical, or title) and true/false:
               lode task set checklist 0 true WL-5
               lode task set checklist "write tests" false WL-5`,
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: taskSetArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			field, values, ref := args[0], args[1:len(args)-1], args[len(args)-1]
			switch field {
			case "state":
				if len(values) != 1 {
					return fmt.Errorf("field \"state\" takes exactly one value: lode task set state <state> <id>")
				}
				if !slices.Contains(model.SettableTaskStates, values[0]) {
					return fmt.Errorf("unknown state %q: must be one of %s",
						values[0], strings.Join(model.SettableTaskStates, ", "))
				}
			case "skills":
			case "checklist":
				if len(values) != 2 {
					return fmt.Errorf("field \"checklist\" takes an item and true/false: lode task set checklist <ordinal|title> <true|false> <id>")
				}
				if _, err := strconv.ParseBool(values[1]); err != nil {
					return fmt.Errorf("checklist checked value %q must be true or false", values[1])
				}
			default:
				return fmt.Errorf("unknown field %q: settable fields are %s", field, strings.Join(taskSetFields, ", "))
			}
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), ref, c, cfg)
			if err != nil {
				return err
			}
			if field == "skills" {
				// Pinning skills does not end the work, so the worktree keeps
				// its task stamp.
				raw, err := c.SetTaskSkills(cmd.Context(), id, values)
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
				cli.PinnedSkillList(cmd.OutOrStdout(), resp.Skills)
				return nil
			}
			if field == "checklist" {
				checked, _ := strconv.ParseBool(values[1]) // validated above
				in := model.SetChecklistItemInput{Checked: checked}
				if ord, err := strconv.Atoi(values[0]); err == nil {
					in.Ordinal = &ord
				} else {
					in.Title = &values[0]
				}
				item, raw, err := c.SetChecklistItem(cmd.Context(), id, in)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				cli.ChecklistRender(cmd.OutOrStdout(), []model.ChecklistItem{item})
				return nil
			}
			t, raw, err := c.SetTaskState(cmd.Context(), id, values[0])
			if err != nil {
				return err
			}
			// The work on this task is over, so drop the current worktree's
			// stamp exactly as the transition commands with clearBinding do.
			clearTaskBindingIfCurrent(cmd, id)
			return renderTask(cmd, t, raw)
		},
	}
}

func newTaskAbandonCmd() *cobra.Command {
	return newTaskTransitionCmd("abandon <id>", "Abandon a task from any non-terminal state",
		true, (*cli.Client).AbandonTask)
}

// newTaskDeleteCmd is `lode task delete` (044 §5): the narrow close for a row
// that should not have existed. Whether the justification is required depends
// on the instance environment and is the server's call (044 §3) — nothing is
// validated or prompted for here, and the server's refusal is returned
// unchanged because its message already names the environment.
func newTaskDeleteCmd() *cobra.Command {
	var justification string
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a task: hide a row that should not have existed",
		Long: "Delete a task. Prefer `lode task abandon`, which keeps the decision\n" +
			"record that work was considered and dropped; delete is for a row that\n" +
			"should not have existed at all (044 §1). The row is tombstoned, not\n" +
			"removed: its events stay in the log, and `lode task undelete` restores\n" +
			"it. A prod instance refuses a delete carrying no --justification.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			t, raw, err := c.DeleteTask(cmd.Context(), id, justification)
			if err != nil {
				return err
			}
			clearTaskBindingIfCurrent(cmd, id)
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s: %s\n", t.ID, t.Title)
			if t.Tombstone != nil && t.Tombstone.Justification != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "reason: %s\n", t.Tombstone.Justification)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&justification, "justification", "",
		"why this row should not have existed (required on a prod instance)")
	return cmd
}

// newTaskUndeleteCmd is `lode task undelete`: clear the tombstone. It takes no
// justification on either instance — only hiding a record is worth making
// someone stop and type (044 §3).
func newTaskUndeleteCmd() *cobra.Command {
	return newTaskTransitionCmd("undelete <id>",
		"Restore a deleted task, clearing its tombstone",
		false, (*cli.Client).UndeleteTask)
}

func newTaskBlockCmd() *cobra.Command {
	return newTaskEdgeCmd("block <id>",
		"Record that another task blocks this one",
		"by", "id of the blocking task (required)",
		"%s is now blocked by %s", (*cli.Client).Block)
}

// newTaskBlockersCmd is `lode task blockers [id]`: the transitive blocker
// tree. `lode task brief` names what holds a task one hop deep; this follows
// each of those down to the tasks nothing holds, which are the ones actually
// claimable.
//
// Without an id it prints the whole scope's forest — one tree per blocked
// task nothing else in scope already lists as a blocker — which is the
// project-wide view `lode board` gives one hop deep.
func newTaskBlockersCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:               "blockers [id]",
		Short:             "Show what transitively blocks a task, or every blocked task in scope, as a tree",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				f, raw, err := c.BlockerForest(cmd.Context(), sc.Project)
				if err != nil {
					return err
				}
				if jsonOut(cmd) {
					printRaw(cmd, raw)
					return nil
				}
				cli.BlockerForestRender(cmd.OutOrStdout(), f)
				return nil
			}
			id, err := resolveTaskIDInScope(cmd.Context(), args[0], c, sc)
			if err != nil {
				return err
			}
			t, raw, err := c.BlockerTree(cmd.Context(), id)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.BlockerTreeRender(cmd.OutOrStdout(), t)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "show one project's blocked tasks")
	return cmd
}

// newTaskFrontierCmd wires `lode task frontier`: the ranked ready set,
// pre-sorted by the D9 ordering the backbone computes (spec 007 §3.4).
func newTaskFrontierCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:   "frontier",
		Short: "Ready, unblocked tasks in pickup order",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}
			resp, raw, err := c.Frontier(cmd.Context(), sc.Project)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			cli.FrontierTable(cmd.OutOrStdout(), resp.Tasks)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "list one project's frontier")
	return cmd
}

func newTaskBriefCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "brief <id>",
		Short:             "Fetch a task's brief: body, branch, open blockers, and active lease",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			cli.BriefRender(cmd.OutOrStdout(), b)
			return nil
		},
	}
	return cmd
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
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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
			cli.TaskCostRender(cmd.OutOrStdout(), tc, costWindowLabel(days))
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 0, "cost window in days, counting today; 0 for all history")
	cmd.Flags().BoolVar(&children, "children", false, "include the task's child_of descendants' sessions")
	return cmd
}

func newTaskUnblockCmd() *cobra.Command {
	return newTaskEdgeCmd("unblock <id>",
		"Remove a blocking edge from another task",
		"by", "id of the blocking task (required)",
		"%s is no longer blocked by %s", (*cli.Client).Unblock)
}

func newTaskParentCmd() *cobra.Command {
	return newTaskEdgeCmd("parent <id>",
		"File a task under a parent task",
		"under", "id of the parent to file it under (required)",
		"%s is now a child of %s", (*cli.Client).Parent)
}

func newTaskUnparentCmd() *cobra.Command {
	return newTaskUnEdgeCmd("unparent <id>", "Detach a task from its parent",
		"%s is no longer a child of %s",
		func(t model.TaskDetail) (string, error) {
			if t.Hierarchy.Parent == nil {
				return "", fmt.Errorf("%s has no parent", t.Task.ID)
			}
			return t.Hierarchy.Parent.ID, nil
		}, (*cli.Client).Unparent)
}

func newTaskFollowUpCmd() *cobra.Command {
	return newTaskEdgeCmd("follow-up <id>",
		"Record that a task was spun out of the work on another task",
		"of", "id of the task this one was spun out of (required)",
		"%s is now a follow-up to %s", (*cli.Client).FollowUp)
}

func newTaskUnfollowUpCmd() *cobra.Command {
	return newTaskUnEdgeCmd("unfollow-up <id>", "Drop a task's follow-up edge to its origin",
		"%s is no longer a follow-up to %s",
		func(t model.TaskDetail) (string, error) {
			for _, e := range t.Edges.Out {
				if e.Type == "follow_up_to" {
					return e.To, nil
				}
			}
			return "", fmt.Errorf("%s is not a follow-up to anything", t.Task.ID)
		}, (*cli.Client).UnfollowUp)
}

func newTaskDuplicateCmd() *cobra.Command {
	cmd := newTaskEdgeCmd("duplicate <id>",
		"Mark a task as a duplicate of the canonical task for the same request",
		"of", "id of the canonical task this one duplicates (required)",
		"%s is now marked a duplicate of %s; it stays claimable until you close it",
		(*cli.Client).Duplicate)
	// The confirmation names the second half because the edge is provenance,
	// not scheduling (004): marking costs nothing and gates nothing, so a
	// message that stopped at "is now marked" reads as if triage were done
	// while `lode work next` is still handing the duplicate out.
	//
	// The verb reads as "copy this task" to anyone who has not met the edge;
	// the alias is the spelling that cannot.
	cmd.Aliases = []string{"dupe"}
	return cmd
}

func newTaskUnduplicateCmd() *cobra.Command {
	cmd := newTaskUnEdgeCmd("unduplicate <id>", "Drop a task's duplicate edge to its canonical task",
		"%s is no longer marked a duplicate of %s",
		func(t model.TaskDetail) (string, error) {
			for _, e := range t.Edges.Out {
				if e.Type == "duplicate_of" {
					return e.To, nil
				}
			}
			return "", fmt.Errorf("%s is not marked a duplicate of anything", t.Task.ID)
		}, (*cli.Client).Unduplicate)
	cmd.Aliases = []string{"undupe"}
	return cmd
}

func newTaskTreeCmd() *cobra.Command {
	var scope scopeFlags
	cmd := &cobra.Command{
		Use:               "tree [id]",
		Short:             "Show tasks with children, and their children, with per-parent progress",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}

			sc, err := resolveScope(cmd.Context(), cmd, c, cfg, &scope)
			if err != nil {
				return err
			}

			// One request for the whole tree: the server picks the containers,
			// rolls up their progress, and returns their children with them
			// (WL-169). An id narrows it to that container's subtree.
			f := cli.TaskTreeFilter{Project: sc.Project, States: resolveStatusFilter(nil)}
			if len(args) == 1 {
				id, err := resolveTaskIDInScope(cmd.Context(), args[0], c, sc)
				if err != nil {
					return err
				}
				f.Root = id
			}
			resp, _, err := c.TaskTree(cmd.Context(), f)
			if err != nil {
				return err
			}
			cli.TreeRender(cmd.OutOrStdout(), resp.Nodes)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, "project id")
	return cmd
}

func newTaskDecomposeCmd() *cobra.Command {
	var into []string
	cmd := &cobra.Command{
		Use:               "decompose <id>",
		Short:             "Split an oversized task into children, in place",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: taskIDAt(0),
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

// newTaskAttachCmd is `lode task attach`: upload one or more files and
// reference them from a task. Images and videos (blobref.Embeddable) are
// appended to the body as markdown so they render inline; everything else is
// attached only (spec 021 §3).
func newTaskAttachCmd() *cobra.Command {
	var noEmbed bool
	var alt string
	cmd := &cobra.Command{
		Use:   "attach <task-id> <file>...",
		Short: "Upload files and attach them to a task",
		Long: "Images and videos are appended to the task body as markdown so they render\n" +
			"inline; every other type is attached only. Use - to read one blob from stdin,\n" +
			"which pairs with a clipboard tool: pngpaste - | lode task attach WL-42 -\n\n" +
			"--alt supplies real alt text for the embedded image; without it, the\n" +
			"reference falls back to the filename, which is not alt text (spec 021 Q021.1).\n" +
			"It applies to one embedded image at a time -- attach images individually when\n" +
			"supplying --alt for more than one.",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(ctx, args[0], c, cfg)
			if err != nil {
				return err
			}

			task, _, err := c.GetTask(ctx, id)
			if err != nil {
				return err
			}
			body := task.Body
			var appended, altUsed bool

			for _, path := range args[1:] {
				var blob model.BlobResponse
				name := filepath.Base(path)
				if path == "-" {
					data, err := io.ReadAll(cmd.InOrStdin())
					if err != nil {
						return fmt.Errorf("read stdin: %w", err)
					}
					if len(data) == 0 {
						return fmt.Errorf("stdin is empty")
					}
					name = "pasted"
					blob, err = c.UploadBlob(ctx, bytes.NewReader(data), int64(len(data)))
					if err != nil {
						return err
					}
				} else {
					blob, err = c.UploadFile(ctx, path)
					if err != nil {
						return err
					}
				}

				if !noEmbed && blobref.Embeddable(blob.MediaType) {
					altText := name
					if alt != "" {
						if altUsed {
							return fmt.Errorf(
								"--alt applies to one embedded image; attach images individually to give each its own alt text")
						}
						altText = alt
						altUsed = true
					}
					if body != "" && !strings.HasSuffix(body, "\n") {
						body += "\n"
					}
					body += "\n" + embedMarkup(altText, blob) + "\n"
					appended = true
					fmt.Fprintf(cmd.OutOrStdout(), "embedded %s (%s)\n", name, blob.Hash[:12])
					continue
				}
				if err := c.AttachBlob(ctx, id, blob.Hash, name); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "attached %s (%s)\n", name, blob.Hash[:12])
			}

			if appended {
				if _, _, err := c.EditTask(ctx, id, model.EditTaskInput{Body: &body}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noEmbed, "no-embed", false,
		"attach images without appending them to the body")
	cmd.Flags().StringVar(&alt, "alt", "",
		"alt text for the embedded image (default: the filename, which is not alt text)")
	return cmd
}

// embedMarkup renders the body fragment that makes a freshly uploaded blob
// render in place (spec 021 §5, §9).
//
// An image is markdown. A video cannot be — `![](…)` is an <img>, which shows
// a video file as a broken image — so it is the raw <video> the sanitiser's
// allowlist exists for (spec 021 §8), carrying the poster frame the upload
// extracted. Without that poster the element is a black rectangle until
// someone presses play, which is a poor answer to "show me the bug".
//
// preload="metadata" rather than the default: the poster is already the still
// frame, so fetching the video body before anyone asks to watch it buys
// nothing and costs a screen recording per page view.
//
// alt is an image concept and is dropped for a video, which is why --alt's
// help says "image": <video> has no alt attribute, and a title tooltip is not
// an accessible name. A video's description belongs in the prose around it.
func embedMarkup(alt string, blob model.BlobResponse) string {
	if !blobref.Video(blob.MediaType) {
		return fmt.Sprintf("![%s](%s)", alt, blob.URL)
	}
	poster := ""
	if blob.PosterURL != "" {
		poster = fmt.Sprintf(" poster=%q", blob.PosterURL)
	}
	return fmt.Sprintf("<video src=%q%s controls preload=\"metadata\"></video>", blob.URL, poster)
}

// newTaskDetachCmd is `lode task detach`: clear an explicit reference from a
// task to an already-uploaded blob. A row the body still embeds survives
// with only its declared half cleared (spec 021 §3), so a still-embedded
// hash gets a warning rather than a silent no-op.
func newTaskDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "detach <task-id> <hash>",
		Short:             "Remove an attached blob from a task",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: taskIDAt(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			id, err := resolveTaskID(cmd.Context(), args[0], c, cfg)
			if err != nil {
				return err
			}
			refs, err := c.ListTaskBlobs(cmd.Context(), id)
			if err != nil {
				return err
			}
			for _, r := range refs {
				if r.Hash == args[1] && r.Embedded {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"warning: the body still embeds %s; it stays until the body stops citing it\n",
						args[1][:12])
				}
			}
			return c.DetachBlob(cmd.Context(), id, args[1])
		},
	}
}
