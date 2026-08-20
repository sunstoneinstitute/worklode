package hookrun

import (
	"bytes"
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

// maxSquashScan bounds how many commits one event hashes for the squash
// probe. The window is what this single event added to the default branch —
// one commit for a local squash, a day's work for a pull — so the cap only
// bites on an unusually large fetch, where the report says so and delivery
// falls back to the webhook.
const maxSquashScan = 200

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
	probe := &mergeProbe{opts: opts, root: root, prev: prev}
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
		if probe.landedNow(t.Branch) {
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

// mergeProbe answers "did this branch's work land in HEAD with this event?"
// once per candidate branch, holding the one part of the answer that is
// per-event rather than per-branch: the patch ids of the commits this event
// added, computed lazily and at most once.
type mergeProbe struct {
	opts   Options
	root   string
	prev   string          // the commit HEAD was at before this event
	landed map[string]bool // patch ids added by this event; see landedPatches
	loaded bool
}

// landedNow reports whether branch's work is in HEAD now and was not in prev
// (the commit this event built on).
//
// Three ways for work to arrive, cheapest first; each runs only for a branch
// the one before it rejected:
//
//   - branch is an ancestor of HEAD — a true merge or a fast-forward, SHAs
//     intact.
//   - every commit in branch has a patch-equivalent commit in HEAD — commits
//     replayed one for one, as a rebase or a run of cherry-picks does.
//   - branch's combined diff is one of the commits this event added — a
//     squash, where N commits collapsed into one and nothing matches
//     commit-for-commit.
//
// The prev test is what keeps a freshly created, still-empty task branch from
// being read as delivered. Such a branch points at a commit that was already
// on the default branch, so every merge would otherwise report every idle
// worktree's task as merged. Excluding what prev already contained also makes
// the handler self-limiting: the commit after a merge does not re-report it.
func (p *mergeProbe) landedNow(branch string) bool {
	if gitOK(p.root, "merge-base", "--is-ancestor", branch, p.prev) {
		return false // already delivered before this event
	}
	if gitOK(p.root, "merge-base", "--is-ancestor", branch, "HEAD") {
		return true
	}
	return p.replayLanded(branch) || p.squashLanded(branch)
}

// replayLanded reports whether every commit in branch has a patch-equivalent
// commit already in HEAD. `git cherry` prints one line per commit, "+" for
// one with no equivalent upstream and "-" for one whose patch is already
// there, so "landed" means no "+" line, not no output.
func (p *mergeProbe) replayLanded(branch string) bool {
	out, err := exec.Command("git", "-C", p.root, "cherry", "HEAD", branch).Output() //nolint:gosec // branch comes from the backbone
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "+") {
			return false
		}
	}
	return true
}

// squashLanded reports whether branch's combined diff arrived as one of the
// commits this event added — a squash merge.
//
// replayLanded cannot see this: it matches patch ids commit by commit, so
// squashing more than one commit into one matches nothing and every commit
// comes back "+". Hashing the branch's whole diff instead matches the squash
// however many commits went into it — which is the common case, "Squash and
// merge" on a branch with more than one commit being GitHub's default.
//
// Scoping the match to the commits this event added is what keeps it from
// re-reporting: a squashed branch is never an ancestor of anything, so the
// prev test above cannot retire it, but the squash commit sits in this
// event's window exactly once.
func (p *mergeProbe) squashLanded(branch string) bool {
	base, ok := gitLine(p.root, "merge-base", p.prev, branch)
	if !ok {
		return false // unrelated history: no combined diff to speak of
	}
	ids := gitPatchIDs(p.root, nil, "diff-tree", "-p", "-r", "--no-renames", "--no-color", base, branch)
	if len(ids) != 1 {
		return false // an empty diff is not a delivery
	}
	return p.landedPatches()[ids[0]]
}

// landedPatches returns the patch ids of the commits this event added to the
// default branch, computing them at most once per event. Merges are skipped:
// they carry no diff of their own, and a squash is never one.
func (p *mergeProbe) landedPatches() map[string]bool {
	if p.loaded {
		return p.landed
	}
	p.loaded = true
	out, err := exec.Command("git", "-C", p.root, "rev-list", "--no-merges", p.prev+"..HEAD").Output() //nolint:gosec // fixed argv, local shas only
	if err != nil {
		return nil
	}
	revs := strings.Fields(string(out))
	if len(revs) == 0 {
		return nil // a merge commit alone adds no diff a squash could match
	}
	if len(revs) > maxSquashScan {
		warn(p.opts, "squash probe reads the newest %d of the %d commits this event added; an older squash falls back to the webhook",
			maxSquashScan, len(revs))
		revs = revs[:maxSquashScan]
	}
	stdin := []byte(strings.Join(revs, "\n") + "\n")
	p.landed = make(map[string]bool, len(revs))
	for _, id := range gitPatchIDs(p.root, stdin, "diff-tree", "--stdin", "-p", "-r", "--no-renames", "--no-color") {
		p.landed[id] = true
	}
	return p.landed
}

// gitPatchIDs runs a diff-producing git command and hashes its output with
// `git patch-id`, the same normalizing hash `git cherry` compares — it
// ignores whitespace, so a squash that reflows still matches. Rename
// detection is forced off on both sides of every comparison so the answer
// does not depend on the repo's diff config.
//
// It returns one id per patch, in order, and nothing at all for an empty
// diff: patch-id prints a line only for a patch that changes something.
func gitPatchIDs(root string, stdin []byte, args ...string) []string {
	diff := exec.Command("git", append([]string{"-C", root}, args...)...) //nolint:gosec // fixed argv, caller-controlled refs only
	diff.Stdin = bytes.NewReader(stdin)
	patch, err := diff.Output()
	if err != nil || len(patch) == 0 {
		return nil
	}
	hash := exec.Command("git", "-C", root, "patch-id", "--stable")
	hash.Stdin = bytes.NewReader(patch)
	out, err := hash.Output()
	if err != nil {
		return nil
	}
	var ids []string
	for line := range strings.SplitSeq(string(out), "\n") {
		// Each line is "<patch id> <commit id>"; the commit id is zero for a
		// diff fed in without its commit header.
		if id, _, found := strings.Cut(strings.TrimSpace(line), " "); found && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
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
	for line := range strings.SplitSeq(string(out), "\n") {
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
