package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
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
// repo-local worktree_dir (cli.WorktreeDirFrom), never the user-level config
// — spec 030 §4 scopes worktree_dir to the checkout, and internal/hookrun's
// guard resolves it the same way. Resolving from cfg.WorktreeDir (the merged,
// user-config-inclusive value) would let a user-level setting silently
// diverge from what every hook guard sees, which fails closed and quiet: the
// CLI would create worktrees under one base while hooks NOP on another. A
// misconfigured worktree_dir is a user error worth reporting, not a silent
// fallback.
func layoutFrom(dir string) (worktree.Layout, error) {
	return worktree.NewLayout(cli.WorktreeDirFrom(dir))
}

// resolveWorktreeTask resolves dir to its enclosing git worktree root and the
// task id encoded in its <base>/<branch> path. It errors when dir is not
// inside a git repository, or when the repo root is not a Worklode worktree.
func resolveWorktreeTask(l worktree.Layout, dir string) (taskID, root string, err error) {
	root, ok := worktree.Root(dir)
	if !ok {
		return "", "", fmt.Errorf("%s is not inside a git repository", dir)
	}
	taskID, ok = l.ParseDir(root)
	if !ok {
		return "", "", fmt.Errorf("%s is not a Worklode worktree (%s/<branch>); run this from inside one", root, l.Base())
	}
	return taskID, root, nil
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
	out, err := exec.Command("git", "-C", root, "worktree", "add", "-b", branch, dir).CombinedOutput()
	if err == nil {
		return nil
	}
	out2, err2 := exec.Command("git", "-C", root, "worktree", "add", dir, branch).CombinedOutput()
	if err2 == nil {
		return nil
	}
	return fmt.Errorf("git worktree add -b %s %s: %s (retry attaching existing branch also failed: %s)",
		branch, dir, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
}

// rollbackClaim undoes a `lode next` claim after a later step (worktree add,
// rebind, or brief fetch) fails: it releases the lease and best-effort
// removes a half-created worktree. Both steps are best-effort — the caller
// is already returning the original error and this must not mask it.
func rollbackClaim(ctx context.Context, c *cli.Client, taskID, root, dir string) {
	if dir != "" {
		exec.Command("git", "-C", root, "worktree", "remove", "--force", dir).Run() //nolint:errcheck
	}
	c.ReleaseLease(ctx, taskID) //nolint:errcheck
}

// newNextCmd builds `lode next`.
func newNextCmd() *cobra.Command {
	var scope scopeFlags
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
			return runNext(cmd, id, &scope, strictFocus)
		},
	}
	addScopeFlags(cmd, &scope, "restrict the pick to a project (only without an id)")
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

func runNext(cmd *cobra.Command, id string, scope *scopeFlags, strictFocus bool) error {
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
	if inside, ok := layout.ParseDir(root); ok {
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
		resp, _, err := c.ClaimNext(ctx, cli.ClaimNextInput{Project: sc.Project, StrictFocus: strictFocus, Worktree: pending})
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

	if err := addWorktree(root, dir, branch); err != nil {
		rollbackClaim(ctx, c, taskID, root, dir)
		return fmt.Errorf("set up worktree for %s: %w", taskID, err)
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

	if jsonOut(cmd) {
		out := struct {
			Claimed  bool            `json:"claimed"`
			Worktree string          `json:"worktree"`
			Branch   string          `json:"branch"`
			Brief    json.RawMessage `json:"brief"`
		}{Claimed: true, Worktree: dir, Branch: branch, Brief: json.RawMessage(briefRaw)}
		b, err := json.Marshal(out)
		if err != nil {
			return fmt.Errorf("encode result: %w", err)
		}
		printRaw(cmd, b)
		return nil
	}

	o := cmd.OutOrStdout()
	fmt.Fprintf(o, "claimed %s\n\n", taskID)
	printBrief(cmd, brief)
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
	taskID, root, err := resolveWorktreeTask(layout, dir)
	if err != nil {
		return err
	}
	identity, err := worktree.Identity(root)
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
	if jsonOut(cmd) {
		printRaw(cmd, raw)
		return nil
	}
	printBrief(cmd, brief)
	return nil
}

// newDoneCmd builds `lode done`.
func newDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done",
		Short: "Mark the current worktree's task merged and release its lease",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			layout, err := layoutFrom(".")
			if err != nil {
				return err
			}
			taskID, root, err := resolveWorktreeTask(layout, ".")
			if err != nil {
				return err
			}
			t, raw, err := c.DoneTask(cmd.Context(), taskID)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			o := cmd.OutOrStdout()
			fmt.Fprintf(o, "%s done\n\n", t.ID)
			fmt.Fprintf(o, "  git worktree remove %s\n", root)
			return nil
		},
	}
	return cmd
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
			taskID, _, err := resolveWorktreeTask(layout, ".")
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

// statusResult is the --json shape of `lode status`.
type statusResult struct {
	Worktree      string             `json:"worktree"`
	Task          cli.Task           `json:"task"`
	LeaseState    string             `json:"lease_state"` // held, expired, held_elsewhere, none
	Lease         *cli.Lease         `json:"lease,omitempty"`
	OpenBlockers  []cli.BriefBlocker `json:"open_blockers"`
	SessionMarker bool               `json:"session_marker"`
	Project       string             `json:"project,omitempty"`
	ProjectSource string             `json:"project_source"`
}

// orNone renders an empty scope as "-" rather than a blank column.
func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// leaseState classifies lease relative to identity (this worktree) and now.
func leaseState(lease *cli.Lease, identity string, now time.Time) string {
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
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the current worktree's task, lease, and session-marker state (read-only)",
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
			taskID, root, err := resolveWorktreeTask(layout, ".")
			if err != nil {
				return err
			}
			identity, err := worktree.Identity(root)
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

			wd, err := os.Getwd()
			if err != nil {
				wd = ""
			}
			scope := cli.ResolveScope(cmd.Context(), c, cfg, wd)

			if jsonOut(cmd) {
				b, err := json.Marshal(statusResult{
					Worktree:      root,
					Task:          brief.Task,
					LeaseState:    state,
					Lease:         brief.Lease,
					OpenBlockers:  brief.OpenBlockers,
					SessionMarker: sessionPresent,
					Project:       scope.Project,
					ProjectSource: string(scope.Source),
				})
				if err != nil {
					return fmt.Errorf("encode result: %w", err)
				}
				printRaw(cmd, b)
				return nil
			}

			o := cmd.OutOrStdout()
			fmt.Fprintf(o, "worktree: %s\n", root)
			fmt.Fprintf(o, "project:  %s (%s)\n", orNone(scope.Project), scope.Source)
			fmt.Fprintf(o, "task: %s — %s\n", brief.Task.ID, brief.Task.Title)
			fmt.Fprintf(o, "state: %s   priority: %s\n", brief.Task.State, brief.Task.Priority)
			switch state {
			case "none":
				fmt.Fprintln(o, "lease: none")
			case "held_elsewhere":
				fmt.Fprintf(o, "lease: held elsewhere (%s)\n", brief.Lease.Worktree)
			case "expired":
				fmt.Fprintf(o, "lease: expired at %s (renewed %s)\n",
					brief.Lease.ExpiresAt.Local().Format(time.RFC3339), brief.Lease.RenewedAt.Local().Format(time.RFC3339))
			default:
				fmt.Fprintf(o, "lease: held, expires %s (renewed %s)\n",
					brief.Lease.ExpiresAt.Local().Format(time.RFC3339), brief.Lease.RenewedAt.Local().Format(time.RFC3339))
			}
			if len(brief.OpenBlockers) > 0 {
				fmt.Fprintln(o, "blocked by:")
				for _, blk := range brief.OpenBlockers {
					fmt.Fprintf(o, "  - %s: %s (%s)\n", blk.ID, blk.Title, blk.State)
				}
			}
			marker := "absent"
			if sessionPresent {
				marker = "present"
			}
			fmt.Fprintf(o, "session marker: %s\n", marker)
			return nil
		},
	}
	return cmd
}
