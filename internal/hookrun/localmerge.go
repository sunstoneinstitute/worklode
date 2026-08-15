package hookrun

import (
	"context"
	"os/exec"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// mergeCandidateStates are the task states a landed commit can advance. They
// mirror the from-states store.ResolveDelivery acts on: a draft has no branch
// worth probing, and a task already at a delivered state has nothing to gain.
var mergeCandidateStates = []string{"ready", "in_progress", "in_review"}

// maxMergeCandidates bounds how many branches one event probes, and so how
// many tasks one report can name (the server caps the same number). The local
// branch set is the real limiter — a developer holds a handful of task
// branches, not a project's worth — so reaching this cap means something is
// wrong, and it is reported rather than silently truncated.
const maxMergeCandidates = 100

// handleLocalMerge reports a merge that landed in this clone, so a task
// advances even when nobody pushes to a repo whose GitHub App is wired up.
// Bound to both post-merge and post-commit: `git merge` fires the first,
// while a squash merge (`git merge --squash` + commit) and a `git commit`
// that resolves a merge fire only the second.
//
// It runs in the main clone, where there is no lease, so unlike every other
// handler here it does not resolve a task id from the worktree layout. The
// guard is instead the branch: unless HEAD is the repo's default branch there
// is nothing to report, which is the case inside every Worklode worktree and
// on every feature branch, and costs two local git calls and no network.
//
// Same contract as pre-commit: every failure is a warning, and nothing can
// fail the event that triggered it.
func handleLocalMerge(ctx context.Context, opts Options, dir string) {
	root, ok := worktree.Root(dir)
	if !ok {
		return // not in a git repo ⇒ NOP
	}
	branch, ok := gitLine(root, "symbolic-ref", "--short", "HEAD")
	if !ok {
		return // detached HEAD, or mid-rebase: nothing landed on a branch
	}
	def, ok := defaultBranch(root)
	if !ok || branch != def {
		return
	}
	head, ok := gitLine(root, "rev-parse", "HEAD")
	if !ok {
		return
	}
	// The commit before this event. Everything already contained in it was
	// delivered by some earlier event, not this one — see landedNow.
	prev, hasPrev := gitLine(root, "rev-parse", "HEAD~1")
	if !hasPrev {
		return // root commit: nothing can have landed into it
	}
	remote, ok := gitLine(root, "remote", "get-url", "origin")
	if !ok {
		return // no origin ⇒ no repo identity the backbone would recognize
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}
	lctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	resp, _, err := c.ListTasks(lctx, cli.TaskListFilter{Repo: remote, States: mergeCandidateStates})
	cancel()
	if err != nil {
		warn(opts, "list merge candidates for %s: %v", remote, err)
		return
	}

	// Intersect with the branches that actually exist here before probing:
	// one for-each-ref replaces a git subprocess per open task in the
	// project, and a branch that is gone cannot be probed anyway (a merge
	// whose branch was already deleted falls back to the webhook).
	local := localBranches(root)
	var landed []string
	for _, t := range resp.Tasks {
		if t.Branch == "" || !local[t.Branch] {
			continue
		}
		if len(landed) >= maxMergeCandidates {
			warn(opts, "more than %d task branches landed in %s; reporting the first %d",
				maxMergeCandidates, head, maxMergeCandidates)
			break
		}
		if landedNow(root, t.Branch, prev) {
			landed = append(landed, t.ID)
		}
	}
	if len(landed) == 0 {
		return
	}

	rctx, cancel := context.WithTimeout(ctx, backboneTimeout)
	defer cancel()
	if _, _, err := c.ReportMerge(rctx, remote, head, landed); err != nil {
		warn(opts, "report merge %s: %v", head, err)
	}
}

// landedNow reports whether branch's work is in HEAD now and was not in prev
// (the commit this event built on).
//
// Two ways for work to arrive, ancestry first because it is the cheap and
// common one; the patch-id walk runs only for a branch ancestry rejects:
//
//   - branch is an ancestor of HEAD — a true merge or a fast-forward, SHAs
//     intact.
//   - `git cherry` finds no commit in branch that HEAD lacks — a squash,
//     where the patches landed and the SHAs did not. It prints one line per
//     commit, "+" for one with no equivalent upstream and "-" for one whose
//     patch is already there, so "landed" means no "+" line, not no output.
//
// Rebase is deliberately out of scope: it neither preserves SHAs nor,
// reliably, patch ids.
//
// The prev test is what keeps a freshly created, still-empty task branch from
// being read as delivered. Such a branch points at a commit that was already
// on the default branch, so every merge would otherwise report every idle
// worktree's task as merged. Excluding what prev already contained also makes
// the handler self-limiting: the commit after a merge does not re-report it.
func landedNow(root, branch, prev string) bool {
	if gitOK(root, "merge-base", "--is-ancestor", branch, prev) {
		return false // already delivered before this event
	}
	if gitOK(root, "merge-base", "--is-ancestor", branch, "HEAD") {
		return true
	}
	out, err := exec.Command("git", "-C", root, "cherry", "HEAD", branch).Output() //nolint:gosec // branch comes from the backbone
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "+") {
			return false
		}
	}
	return true
}

// defaultBranch resolves the repo's default branch from origin/HEAD, the ref
// `git clone` sets and `git remote set-head origin -a` repairs.
//
// A clone without it falls back to the conventional names, accepting one of
// them only when it is the sole candidate present: guessing wrong here would
// report merges from a long-lived branch as deliveries, so an ambiguous repo
// reports nothing and leaves delivery to the webhook.
func defaultBranch(root string) (string, bool) {
	if ref, ok := gitLine(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); ok {
		if _, name, found := strings.Cut(ref, "/"); found && name != "" {
			return name, true
		}
	}
	local := localBranches(root)
	var found string
	for _, name := range []string{"main", "master"} {
		if !local[name] {
			continue
		}
		if found != "" {
			return "", false // ambiguous
		}
		found = name
	}
	return found, found != ""
}

// localBranches returns the set of branch names in root, in one git call.
func localBranches(root string) map[string]bool {
	out, err := exec.Command("git", "-C", root, "for-each-ref",
		"--format=%(refname:short)", "refs/heads/").Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	return set
}

// gitLine runs a git command in root and returns its single line of output.
// Empty output counts as failure: every caller wants a name or a sha.
func gitLine(root string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output() //nolint:gosec // fixed argv, caller-controlled refs only
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(out))
	return line, line != ""
}

// gitOK runs a git command in root for its exit status alone.
func gitOK(root string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", root}, args...)...).Run() == nil //nolint:gosec // fixed argv, caller-controlled refs only
}
