package cmd

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/harness"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// This file implements the worktree-aware lifecycle commands: the top-level
// `lode next`, `resume`, `done`, `block`, and `status`. Unlike the lower-level
// `lode task claim/renew/release/done/block` commands (task.go), these are
// the ONE way an agent enters, resumes, and exits Worklode work: they own the
// git worktree (creating it on `next`, never removing it themselves) and
// speak in terms of "the task in the worktree I'm standing in" rather than an
// explicit task id.

func init() {
	rootCmd.AddCommand(newNextCmd(), newResumeCmd(), newDoneCmd(), newBlockCmd(), newStatusCmd())
}

// layoutFrom builds the worktree layout for dir's repo. It reads ONLY the
// repo-local worktree_dir (cli.WorktreeDirFrom), never the merged
// user-level value — spec 008 §6 scopes worktree_dir to the checkout, and
// internal/hookrun's guard resolves it the same way. Reading the merged value
// here would let the CLI create worktrees under one base while every hook
// guard NOPs on another.
func layoutFrom(dir string) (worktree.Layout, error) {
	return worktree.NewLayout(cli.WorktreeDirFrom(dir))
}

// resolveWorktreeTask resolves dir to its enclosing git worktree root and its
// task id — the explicit worklode.task-id git config when the worktree carries
// one, else the <base>/<branch> path. It errors when dir is not inside a git
// repository, or when the repo root carries no task binding.
//
// byName is the caller's own "say which task instead of inferring it" form —
// an id for the commands that have an explicit-id sibling ("lode task done
// <id>"), a directory for `resume`. It is offered first in the failure because
// the user is standing in a checkout that holds no task, and the shortest way
// forward is usually to name the one they mean. Callers with no such form
// pass "".
func resolveWorktreeTask(l worktree.Layout, dir, byName string) (taskID, root string, err error) {
	root, ok := worktree.Root(dir)
	if !ok {
		return "", "", fmt.Errorf("%s is not inside a git repository", dir)
	}
	taskID, ok = l.TaskID(root)
	if !ok {
		return "", "", fmt.Errorf("%s is not bound to a Worklode task\n\n%s", root, unboundHelp(l, byName))
	}
	return taskID, root, nil
}

// unboundHelp renders the two ways out of an unbound checkout: say which task,
// or claim one into a worktree that carries the binding. The description
// column is sized to the widest form so a long one (`lode task block <id>
// --by <blocker-id>`) does not shear the alignment.
func unboundHelp(l worktree.Layout, byName string) string {
	const claim = "lode next [id]"
	width := max(len(claim), len(byName))
	var b strings.Builder
	if byName != "" {
		fmt.Fprintf(&b, "  %-*s  say which task to act on\n", width, byName)
	}
	fmt.Fprintf(&b, "  %-*s  claim a task and work it under %s/\n", width, claim, l.Base())
	return b.String()
}

// pendingIdentity returns a temporary lease-worktree identity for a claim
// that has not yet materialized a real git worktree:
// "<hostname>:<root>#pending-<8hex>".
func pendingIdentity(root string) (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("determine hostname: %w", err)
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate pending worktree id: %w", err)
	}
	return fmt.Sprintf("%s:%s#pending-%s", host, root, hex.EncodeToString(buf)), nil
}

// addWorktree creates the worktree at dir on branch, creating the branch if
// it does not already exist. If -b fails because branch already exists (a
// leftover from an earlier attempt), it retries attaching to the existing
// branch instead.
func addWorktree(root, dir, branch string) error {
	err := gitexec.Run(root, "worktree", "add", "-b", branch, dir)
	if err == nil {
		return nil
	}
	err2 := gitexec.Run(root, "worktree", "add", dir, branch)
	if err2 == nil {
		return nil
	}
	return fmt.Errorf("%w (retry attaching existing branch also failed: %v)", err, err2)
}

// clearTaskBinding drops the worklode.task-id stamp from a checkout whose
// lease has just ended, so a worktree left on disk after the work is over no
// longer answers for that task. Best-effort by design: the command it trails
// has already succeeded on the server, and a checkout under <base>/<branch>
// still resolves by directory name regardless (worktree.UnsetTaskID).
func clearTaskBinding(cmd *cobra.Command, root string) {
	if err := worktree.UnsetTaskID(root); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: clear task id in worktree git config: %v\n", err)
	}
}

// clearTaskBindingIfCurrent is the explicit-id counterpart to
// clearTaskBinding, for the `lode task ...  <id>` commands that end a lease
// but take the task by name and may be run from anywhere. It clears the stamp
// only when the checkout the command was invoked from is bound to that very
// task: ending WL-5's lease from inside WL-9's worktree must leave WL-9 alone,
// and from an unbound checkout there is nothing to clear. Silent on every
// failure to resolve — this is a side effect of a command that has already
// succeeded, never a reason to report one.
func clearTaskBindingIfCurrent(cmd *cobra.Command, taskID string) {
	layout, err := layoutFrom(".")
	if err != nil {
		return
	}
	root, ok := worktree.Root(".")
	if !ok {
		return
	}
	if bound, ok := layout.TaskID(root); !ok || bound != taskID {
		return
	}
	clearTaskBinding(cmd, root)
}

// purgeTaskSecrets drops the task's materialized secrets, reporting failure on
// stderr: the work is finished either way, so a purge error must not fail the
// command that reports it.
func purgeTaskSecrets(cmd *cobra.Command, taskID string) {
	names, err := secrets.PurgeTask(taskID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "secrets: purge: %v\n", err)
		return
	}
	if len(names) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "secrets: purged %s\n", strings.Join(names, ", "))
	}
}

// rollbackClaim undoes a `lode next` claim after a later step (worktree add,
// rebind, or brief fetch) fails: it releases the lease and best-effort
// removes a half-created worktree, clearing the task stamp first in case the
// removal is the step that fails. All three are best-effort — the caller is
// already returning the original error and this must not mask it.
func rollbackClaim(ctx context.Context, c *cli.Client, taskID, root, dir string) {
	if dir != "" {
		worktree.UnsetTaskID(dir) //nolint:errcheck
		gitexec.OK(root, "worktree", "remove", "--force", dir)
	}
	c.ReleaseLease(ctx, taskID) //nolint:errcheck
}

// newNextCmd builds `lode next`.
func newNextCmd() *cobra.Command {
	var scope scopeFlags
	var kind string
	var strictFocus bool
	cmd := &cobra.Command{
		Use:   "next [id]",
		Short: "Claim a task (or the top-ranked ready one), set up its worktree, and print its brief",
		Long: "The one way to enter Worklode mode: claims a task, creates its worktree " +
			"and its task branch, binds the lease to that worktree, and prints " +
			"the task's brief. With an id, claims that task; without one, claims the top-ranked " +
			"ready task (like `lode task claim --next`).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) > 0 {
				id = args[0]
			}
			return runNext(cmd, id, &scope, kind, strictFocus)
		},
	}
	addScopeFlags(cmd, &scope, "restrict the pick to a project (only without an id)")
	cmd.Flags().StringVar(&kind, "kind", "", "restrict the pick to a kind: feature, bug, chore, design, review, spike (only without an id)")
	cmd.Flags().BoolVar(&strictFocus, "strict-focus", false, "restrict the pick to the project's focus concerns only (only without an id)")
	return cmd
}

// slugFromBranch recovers the slug from a "<prefix><id>-<slug>" branch
// without assuming the prefix. The first "<id>-" is the prefix-adjacent one,
// so a slug that repeats the task id stays intact. Falls back to branch
// itself if id is absent.
func slugFromBranch(branch, id string) string {
	if i := strings.Index(branch, id+"-"); i >= 0 {
		return branch[i+len(id)+1:]
	}
	return branch
}

func runNext(cmd *cobra.Command, id string, scope *scopeFlags, kind string, strictFocus bool) error {
	warnDeprecatedTaskKind(cmd, kind)
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	layout, err := layoutFrom(".")
	if err != nil {
		return err
	}

	if id != "" {
		id, err = resolveTaskID(ctx, id, c, cfg)
		if err != nil {
			return err
		}
	}

	root, ok := worktree.Root(".")
	if !ok {
		return fmt.Errorf("not inside a git repository")
	}
	if inside, ok := layout.TaskID(root); ok {
		return fmt.Errorf("already inside a worktree for %s; run `lode next` from the main repository, not from %s/", inside, layout.Base())
	}

	pending, err := pendingIdentity(root)
	if err != nil {
		return err
	}

	// The server is the authority on the branch name (rendered from
	// LODE_BRANCH_TEMPLATE), so both paths take it from the claim response.
	var taskID, slug, branch string
	switch {
	case id != "":
		resp, _, err := c.ClaimTask(ctx, id, pending, 0)
		if err != nil {
			return err
		}
		taskID = id
		branch = resp.Branch
		slug = slugFromBranch(resp.Branch, id)
	default:
		sc, err := resolveScope(ctx, cmd, c, cfg, scope)
		if err != nil {
			return err
		}
		resp, _, err := c.ClaimNext(ctx, model.ClaimNextInput{Project: sc.Project, Kind: kind, StrictFocus: strictFocus, Worktree: pending})
		if err != nil {
			return err
		}
		if !resp.Claimed || resp.Task == nil {
			return printNoReadyTask(cmd)
		}
		taskID = resp.Task.ID
		slug = resp.Task.Slug
		branch = resp.Task.Branch
	}
	if branch == "" {
		branch = worktree.BranchName(taskID, slug)
	}

	dir := layout.Dir(root, branch)

	if err := worktree.EnableWorktreeConfigExtension(root); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: enable git worktree config extension: %v\n", err)
	}

	if err := addWorktree(root, dir, branch); err != nil {
		rollbackClaim(ctx, c, taskID, root, dir)
		return fmt.Errorf("set up worktree for %s: %w", taskID, err)
	}

	// Printed as soon as the worktree exists, not only on full success — a
	// later failure (e.g. the brief fetch) rolls the worktree back via
	// rollbackClaim, and the operator should still see where it was before
	// that happened.
	if !jsonOut(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "worktree: %s\n", dir)
	}

	if err := worktree.SetTaskID(dir, taskID); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: stamp task id in worktree git config: %v\n", err)
	}

	if err := (harness.ClaudeCode{}).PropagateToWorktree(root, dir); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: mirror Claude Code hooks into worktree: %v\n", err)
	}

	identity, err := worktree.Identity(dir)
	if err != nil {
		rollbackClaim(ctx, c, taskID, root, dir)
		return fmt.Errorf("resolve worktree identity: %w", err)
	}

	if _, _, err := c.RebindWorktree(ctx, taskID, identity); err != nil {
		rollbackClaim(ctx, c, taskID, root, dir)
		return fmt.Errorf("bind lease to worktree: %w", err)
	}

	brief, briefRaw, err := c.Brief(ctx, taskID)
	if err != nil {
		rollbackClaim(ctx, c, taskID, root, dir)
		return fmt.Errorf("fetch brief for %s: %w", taskID, err)
	}

	// Spec 017: consent + materialization while the operator is present.
	// Never fails the claim; writes to stderr only.
	runSecretsCeremony(ctx, cmd, c, taskID, dir, brief.Task.Secrets)

	if jsonOut(cmd) {
		return printJSON(cmd, nextResult{
			Claimed: true, Worktree: dir, Branch: branch,
			Brief: json.RawMessage(briefRaw),
		})
	}

	o := cmd.OutOrStdout()
	fmt.Fprintf(o, "claimed %s\n\n", taskID)
	cli.BriefRender(o, brief)
	fmt.Fprintf(o, "\nworktree: %s\n\n", dir)
	fmt.Fprintf(o, "  cd %s\n", dir)
	return nil
}

// printNoReadyTask reports the "nothing to claim" outcome of `lode next`
// without --id: not an error, just nothing ready.
func printNoReadyTask(cmd *cobra.Command) error {
	if jsonOut(cmd) {
		printRaw(cmd, []byte(`{"claimed":false,"reason":"no-ready-task"}`))
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "no ready task")
	return nil
}

// newResumeCmd builds `lode resume`.
func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume [dir]",
		Short: "Re-acquire the lease on the current (or given) worktree's task",
		Long: "Renews the lease if this worktree still holds it, or re-claims the task if the " +
			"sweeper reclaimed an expired lease (the task is back in ready). Errors if the task " +
			"is actively leased to a different worktree.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return runResume(cmd, dir)
		},
	}
	return cmd
}

func runResume(cmd *cobra.Command, dir string) error {
	c, err := newAPIClient()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	// Resolve the layout from dir, not the process cwd: resume may target a
	// worktree in a different repo (or the same repo with a different base)
	// than the shell it was invoked from (hookrun.go:125-127 flags the same
	// hazard).
	layout, err := layoutFrom(dir)
	if err != nil {
		return err
	}
	taskID, root, err := resolveWorktreeTask(layout, dir, "lode resume <dir>")
	if err != nil {
		return err
	}
	identity, err := worktree.IdentityOf(root)
	if err != nil {
		return err
	}

	// Only brief.Lease is read here, and the full brief follows once the lease
	// is secured — no reason to pay for pins and matching twice.
	brief, _, err := c.BriefWithoutSkills(ctx, taskID)
	if err != nil {
		return err
	}

	if err := cli.ReacquireOrRenew(ctx, c, taskID, identity, brief.Lease); err != nil {
		return err
	}

	brief, raw, err := c.Brief(ctx, taskID)
	if err != nil {
		return err
	}
	if !secretsSatisfied(taskID, brief.Task.Secrets) {
		runSecretsCeremony(ctx, cmd, c, taskID, root, brief.Task.Secrets)
	}
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	cli.BriefRender(cmd.OutOrStdout(), brief)
	return nil
}

// newDoneCmd builds `lode done`: the worktree's "my work here is finished"
// verb. It submits the task for review (in_progress -> in_review) and closes
// the lease; it never moves the task to `merged`. `merged` means the work
// landed on the default branch (spec 004 §5.1) — a fact only the PR-merge
// webhook, the delivery resolver, or a human running `lode task done <id>`
// for a change that carries no PR can know. An agent finishing in a worktree
// knows none of it: the branch may not even be pushed yet.
func newDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done",
		Short: "Submit the current worktree's task for review and release its lease",
		Long: "Submit the current worktree's task for review and release its lease.\n\n" +
			"The task moves to in_review, not merged: `merged` records that the\n" +
			"work landed on the default branch, which the PR-merge webhook reports.\n" +
			"For a change that will never have a PR, close it with `lode task done\n" +
			"<id>` once it has actually landed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			layout, err := layoutFrom(".")
			if err != nil {
				return err
			}
			taskID, root, err := resolveWorktreeTask(layout, ".", "lode task submit <id>")
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			t, raw, err := submitForReview(ctx, c, taskID)
			if err != nil {
				return err
			}
			if _, err := c.ReleaseLease(ctx, taskID); err != nil {
				return fmt.Errorf("submitted %s for review, but failed to release the lease: %w", taskID, err)
			}
			purgeTaskSecrets(cmd, taskID)
			clearTaskBinding(cmd, root)
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			o := cmd.OutOrStdout()
			fmt.Fprintf(o, "%s submitted for review; lease released\n\n", t.ID)
			fmt.Fprintf(o, "  push the branch and open its PR — merging it moves %s to merged\n", t.ID)
			fmt.Fprintf(o, "  git worktree remove %s\n", root)
			return nil
		},
	}
	return cmd
}

// submitForReview moves taskID to in_review, tolerating a task already there:
// a worker that ran `lode task submit` before `lode done` should still get its
// lease released rather than a transition error. Any other refusal is the
// server's to report, unchanged.
func submitForReview(ctx context.Context, c *cli.Client, taskID string) (model.Task, []byte, error) {
	t, raw, err := c.SubmitTask(ctx, taskID)
	if err == nil {
		return t, raw, nil
	}
	detail, _, getErr := c.GetTask(ctx, taskID)
	if getErr != nil || detail.State != "in_review" {
		return model.Task{}, nil, err
	}
	raw, marshalErr := json.Marshal(detail.Task)
	if marshalErr != nil {
		return model.Task{}, nil, err
	}
	return detail.Task, raw, nil
}

// newBlockCmd builds `lode block --on <blocker-id>`.
func newBlockCmd() *cobra.Command {
	var on string
	cmd := &cobra.Command{
		Use:   "block --on <blocker-id>",
		Short: "Record that another task blocks the current worktree's task, and release its lease",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := newAPIClientWithConfig()
			if err != nil {
				return err
			}
			layout, err := layoutFrom(".")
			if err != nil {
				return err
			}
			taskID, root, err := resolveWorktreeTask(layout, ".", "lode task block <id> --by <blocker-id>")
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			on, err = resolveTaskID(ctx, on, c, cfg)
			if err != nil {
				return err
			}
			raw, err := c.Block(ctx, taskID, on)
			if err != nil {
				return err
			}
			if _, err := c.ReleaseLease(ctx, taskID); err != nil {
				return fmt.Errorf("recorded %s blocked by %s, but failed to release the lease: %w", taskID, on, err)
			}
			purgeTaskSecrets(cmd, taskID)
			clearTaskBinding(cmd, root)
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now blocked by %s; lease released\n", taskID, on)
			return nil
		},
	}
	cmd.Flags().StringVar(&on, "on", "", "id of the blocking task (required)")
	cmd.MarkFlagRequired("on")
	return cmd
}

// nextResult is the --json shape of `lode next`. Brief is the brief response
// forwarded verbatim, so the two never disagree about its shape.
type nextResult struct {
	Claimed  bool            `json:"claimed"`
	Worktree string          `json:"worktree"`
	Branch   string          `json:"branch"`
	Brief    json.RawMessage `json:"brief"`
}

// statusResult is the --json shape of `lode status`.
type statusResult struct {
	Worktree      string               `json:"worktree"`
	Task          model.Task           `json:"task"`
	LeaseState    string               `json:"lease_state"` // held, expired, held_elsewhere, none
	Lease         *model.Lease         `json:"lease,omitempty"`
	OpenBlockers  []model.BriefBlocker `json:"open_blockers"`
	BlockingPlans []model.DocRef       `json:"blocking_plans"`
	SessionMarker bool                 `json:"session_marker"`
	Project       string               `json:"project,omitempty"`
	ProjectSource string               `json:"project_source"`
}

// leaseState classifies lease relative to identity (this worktree) and now.
func leaseState(lease *model.Lease, identity string, now time.Time) string {
	switch {
	case lease == nil:
		return "none"
	case lease.Worktree != identity:
		return "held_elsewhere"
	case lease.ExpiresAt.Before(now):
		return "expired"
	default:
		return "held"
	}
}

// hasSessionMarker reports whether worklode-session.json exists in root's
// private git dir. Presence only — no liveness/pid checking.
func hasSessionMarker(root string) bool {
	gitDir, err := worktree.GitDir(root)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(gitDir, "worklode-session.json"))
	return err == nil
}

// newStatusCmd builds `lode status`: read-only, never claims/renews/releases.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current worktree's task, lease, and session-marker state (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd)
		},
	}
}

// runStatus is `lode status`'s body: read the worktree's task, classify its
// lease, and report both with the project the directory scopes to.
func runStatus(cmd *cobra.Command) error {
	c, cfg, err := newAPIClientWithConfig()
	if err != nil {
		return err
	}
	layout, err := layoutFrom(".")
	if err != nil {
		return err
	}
	taskID, root, err := resolveWorktreeTask(layout, ".", "lode task brief <id>")
	if err != nil {
		return err
	}
	identity, err := worktree.IdentityOf(root)
	if err != nil {
		return err
	}
	// status reports the lease, nothing from the brief's skills.
	brief, _, err := c.BriefWithoutSkills(cmd.Context(), taskID)
	if err != nil {
		return err
	}

	sessionPresent := hasSessionMarker(root)
	state := leaseState(brief.Lease, identity, time.Now())

	scope := currentScope(cmd.Context(), c, cfg)

	if jsonOut(cmd) {
		return printJSON(cmd, statusResult{
			Worktree:      root,
			Task:          brief.Task,
			LeaseState:    state,
			Lease:         brief.Lease,
			OpenBlockers:  brief.OpenBlockers,
			BlockingPlans: brief.BlockingPlans,
			SessionMarker: sessionPresent,
			Project:       scope.Project,
			ProjectSource: string(scope.Source),
		})
	}

	o := cmd.OutOrStdout()
	fmt.Fprintf(o, "worktree: %s\n", root)
	fmt.Fprintf(o, "project:  %s (%s)\n", cmp.Or(scope.Project, "-"), scope.Source)
	fmt.Fprintf(o, "task: %s — %s\n", brief.Task.ID, brief.Task.Title)
	fmt.Fprintf(o, "state: %s   priority: %s\n", brief.Task.State, brief.Task.Priority)
	switch state {
	case "none":
		fmt.Fprintln(o, "lease: none")
	case "held_elsewhere":
		fmt.Fprintf(o, "lease: held elsewhere (%s)\n", brief.Lease.Worktree)
	case "expired":
		fmt.Fprintf(o, "lease: expired at %s (renewed %s)\n",
			cli.LocalTime(brief.Lease.ExpiresAt), cli.LocalTime(brief.Lease.RenewedAt))
	default:
		fmt.Fprintf(o, "lease: held, expires %s (renewed %s)\n",
			cli.LocalTime(brief.Lease.ExpiresAt), cli.LocalTime(brief.Lease.RenewedAt))
	}
	cli.BlockersRender(o, brief.OpenBlockers, brief.BlockingPlans)
	marker := "absent"
	if sessionPresent {
		marker = "present"
	}
	fmt.Fprintf(o, "session marker: %s\n", marker)
	return nil
}
