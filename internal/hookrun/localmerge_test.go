package hookrun

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newMergeRepo builds a throwaway clone-shaped repo: a default branch and an
// origin remote, which is all handleLocalMerge's guard looks at.
func newMergeRepo(t *testing.T) *gitRepo {
	t.Helper()
	r := &gitRepo{t: t, root: t.TempDir()}
	r.git("-c", "init.defaultBranch=main", "init")
	r.git("remote", "add", "origin", "git@github.com:acme/app.git")
	r.commit("README.md", "hello\n", "initial commit")
	return r
}

// mergeBackbone answers the two calls the handler makes: the candidate list
// and the merge report. It records every reported body so a test can assert
// what was claimed, and to whom.
type mergeBackbone struct {
	mu       sync.Mutex
	tasks    []map[string]any // what GET /api/v1/tasks returns
	reports  []map[string]any // bodies POSTed to /api/v1/merges
	listQury []string         // raw query strings of the list calls
}

func newMergeBackbone(t *testing.T, tasks []map[string]any) *mergeBackbone {
	t.Helper()
	b := &mergeBackbone{tasks: tasks}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.listQury = append(b.listQury, r.URL.RawQuery)
		b.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": b.tasks})
	})
	mux.HandleFunc("POST /api/v1/merges", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.mu.Lock()
		b.reports = append(b.reports, body)
		b.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", "test-token")
	return b
}

// reported returns the task ids named in the single report, failing when the
// handler reported a different number of times than expected.
func (b *mergeBackbone) reported(t *testing.T) []string {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.reports) != 1 {
		t.Fatalf("merge reports = %d (%v), want exactly 1", len(b.reports), b.reports)
	}
	raw, _ := b.reports[0]["tasks"].([]any)
	ids := make([]string, 0, len(raw))
	for _, v := range raw {
		ids = append(ids, v.(string))
	}
	return ids
}

func (b *mergeBackbone) reportCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.reports)
}

// runGitHook drives one git-side hook event against dir. A hook must never
// fail the event that triggered it, so a non-zero exit is a test failure.
func runGitHook(t *testing.T, event, dir string) {
	t.Helper()
	runHook(t, event, Payload{Cwd: dir})
}

// TestLocalMergeReportsTrueMerge is the ordinary case: `git merge` on the
// default branch, SHAs intact, nothing pushed anywhere.
func TestLocalMergeReportsTrueMerge(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-1", "branch": "WL-1-fix-the-thing", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-1-fix-the-thing")
	r.commit("fix.txt", "fixed\n", "fix the thing")
	r.git("checkout", "main")
	r.git("merge", "--no-ff", "-m", "Merge branch 'WL-1-fix-the-thing'", "WL-1-fix-the-thing")

	runGitHook(t, "post-merge", r.root)

	if got := b.reported(t); len(got) != 1 || got[0] != "WL-1" {
		t.Fatalf("reported %v, want [WL-1]", got)
	}
	// The candidate query is scoped to this repo and to the states a merge
	// can advance — not the whole backlog.
	b.mu.Lock()
	defer b.mu.Unlock()
	q := strings.Join(b.listQury, " ")
	if !strings.Contains(q, "repo=") {
		t.Fatalf("list query %q does not scope by repo", q)
	}
	if !strings.Contains(q, "state=ready") || !strings.Contains(q, "state=in_review") {
		t.Fatalf("list query %q does not scope by open states", q)
	}
}

// TestLocalMergeReportsFastForward: a fast-forward leaves no merge commit,
// but the branch is still an ancestor of HEAD.
func TestLocalMergeReportsFastForward(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-1", "branch": "WL-1-fix-the-thing", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-1-fix-the-thing")
	r.commit("a.txt", "a\n", "first")
	r.commit("b.txt", "b\n", "second")
	r.git("checkout", "main")
	r.git("merge", "--ff-only", "WL-1-fix-the-thing")

	runGitHook(t, "post-merge", r.root)

	if got := b.reported(t); len(got) != 1 || got[0] != "WL-1" {
		t.Fatalf("reported %v, want [WL-1]", got)
	}
}

// TestLocalMergeReportsSquash: a squash rewrites the SHAs, so only the
// patch-id walk can see the work landed — and it fires post-commit, never
// post-merge.
func TestLocalMergeReportsSquash(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-2", "branch": "WL-2-squash-me", "state": "in_progress"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-2-squash-me")
	r.commit("s.txt", "squashed\n", "work")
	r.git("checkout", "main")
	r.git("merge", "--squash", "WL-2-squash-me")
	r.git("commit", "-m", "squash WL-2")

	runGitHook(t, "post-commit", r.root)

	if got := b.reported(t); len(got) != 1 || got[0] != "WL-2" {
		t.Fatalf("reported %v, want [WL-2]", got)
	}
}

// TestLocalMergeIgnoresUntouchedBranch is the false-delivery guard. `lode
// next` creates a branch at the default branch's tip, so an idle worktree's
// branch is an ancestor of HEAD from the moment it exists. Without the
// "not already in the previous commit" test, every merge would report every
// idle task as delivered.
func TestLocalMergeIgnoresUntouchedBranch(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-1", "branch": "WL-1-real-work", "state": "in_review"},
		{"id": "WL-9", "branch": "WL-9-never-started", "state": "in_progress"},
	})
	r := newMergeRepo(t)
	r.git("branch", "WL-9-never-started") // created, never committed to
	r.git("checkout", "-b", "WL-1-real-work")
	r.commit("real.txt", "real\n", "real work")
	r.git("checkout", "main")
	r.git("merge", "--no-ff", "-m", "Merge branch 'WL-1-real-work'", "WL-1-real-work")

	runGitHook(t, "post-merge", r.root)

	got := b.reported(t)
	if len(got) != 1 || got[0] != "WL-1" {
		t.Fatalf("reported %v, want [WL-1] only — WL-9 has no commits of its own", got)
	}
}

// TestLocalMergeDoesNotReportOnTheNextCommit: the same guard makes the
// handler self-limiting. An ordinary commit after a merge must not re-report
// what the merge already reported.
func TestLocalMergeDoesNotReportOnTheNextCommit(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-1", "branch": "WL-1-fix-the-thing", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-1-fix-the-thing")
	r.commit("fix.txt", "fixed\n", "fix the thing")
	r.git("checkout", "main")
	r.git("merge", "--no-ff", "-m", "Merge branch 'WL-1-fix-the-thing'", "WL-1-fix-the-thing")
	runGitHook(t, "post-merge", r.root)

	r.commit("unrelated.txt", "later\n", "an ordinary commit afterwards")
	runGitHook(t, "post-commit", r.root)

	if n := b.reportCount(); n != 1 {
		t.Fatalf("reports = %d, want 1: the commit after a merge must not re-report it", n)
	}
}

// TestLocalMergeIgnoresNonDefaultBranch: this is the cost-control guard, and
// it is what makes the hook free inside every worktree — no candidate list,
// no report, no network at all.
func TestLocalMergeIgnoresNonDefaultBranch(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-1", "branch": "WL-1-side", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "feature")
	r.commit("f.txt", "f\n", "on a feature branch")

	runGitHook(t, "post-commit", r.root)

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.listQury) != 0 || len(b.reports) != 0 {
		t.Fatalf("hook talked to the backbone off the default branch: lists=%v reports=%v",
			b.listQury, b.reports)
	}
}

// TestLocalMergeIgnoresBranchThatIsGone: the accepted gap. A branch deleted
// before the hook runs cannot be probed, and delivery falls back to the
// webhook rather than being guessed at.
func TestLocalMergeIgnoresBranchThatIsGone(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-1", "branch": "WL-1-gone", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-1-gone")
	r.commit("g.txt", "g\n", "work")
	r.git("checkout", "main")
	r.git("merge", "--no-ff", "-m", "Merge branch 'WL-1-gone'", "WL-1-gone")
	r.git("branch", "-D", "WL-1-gone")

	runGitHook(t, "post-merge", r.root)

	if n := b.reportCount(); n != 0 {
		t.Fatalf("reports = %d, want 0: a deleted branch cannot be probed", n)
	}
}

// TestLocalMergeOutsideAGitRepoIsANop: hooks run wherever the user is.
func TestLocalMergeOutsideAGitRepoIsANop(t *testing.T) {
	b := newMergeBackbone(t, nil)
	runGitHook(t, "post-merge", t.TempDir())
	if n := b.reportCount(); n != 0 {
		t.Fatalf("reports = %d outside a git repo, want 0", n)
	}
}

func TestDefaultBranch(t *testing.T) {
	t.Run("from origin/HEAD", func(t *testing.T) {
		r := newMergeRepo(t)
		r.git("checkout", "-b", "trunk")
		r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
		if got, ok := defaultBranch(r.root); !ok || got != "trunk" {
			t.Fatalf("defaultBranch = %q, %v; want trunk, true", got, ok)
		}
	})

	t.Run("falls back to the sole conventional name", func(t *testing.T) {
		r := newMergeRepo(t) // main, and no origin/HEAD
		if got, ok := defaultBranch(r.root); !ok || got != "main" {
			t.Fatalf("defaultBranch = %q, %v; want main, true", got, ok)
		}
	})

	// Guessing wrong would report a long-lived branch's merges as
	// deliveries, so an ambiguous repo reports nothing at all.
	t.Run("ambiguous is no answer", func(t *testing.T) {
		r := newMergeRepo(t)
		r.git("branch", "master")
		if got, ok := defaultBranch(r.root); ok {
			t.Fatalf("defaultBranch = %q, true; want no answer with both main and master present", got)
		}
	})
}

// TestLocalMergeReportsMultiCommitSquash is the case `git cherry` cannot see.
// Squashing two commits into one matches neither of their patch ids, so only
// hashing the branch's combined diff finds the work — and "Squash and merge"
// on a branch with more than one commit is GitHub's default.
func TestLocalMergeReportsMultiCommitSquash(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-3", "branch": "WL-3-two-commits", "state": "in_progress"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-3-two-commits")
	r.commit("a.txt", "a\n", "first")
	r.commit("b.txt", "b\n", "second")
	r.git("checkout", "main")
	r.git("merge", "--squash", "WL-3-two-commits")
	r.git("commit", "-m", "squash WL-3")

	runGitHook(t, "post-commit", r.root)

	if got := b.reported(t); len(got) != 1 || got[0] != "WL-3" {
		t.Fatalf("reported %v, want [WL-3]", got)
	}
}

// TestLocalMergeDoesNotReReportASquash: the squash probe matches only the
// commits this event added, so the commit after a squash must not report it
// again. The ancestry guard cannot do this job — a squashed branch is never
// an ancestor of anything.
func TestLocalMergeDoesNotReReportASquash(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-3", "branch": "WL-3-two-commits", "state": "in_progress"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-3-two-commits")
	r.commit("a.txt", "a\n", "first")
	r.commit("b.txt", "b\n", "second")
	r.git("checkout", "main")
	r.git("merge", "--squash", "WL-3-two-commits")
	r.git("commit", "-m", "squash WL-3")
	runGitHook(t, "post-commit", r.root)

	r.commit("unrelated.txt", "later\n", "an ordinary commit afterwards")
	runGitHook(t, "post-commit", r.root)

	if n := b.reportCount(); n != 1 {
		t.Fatalf("reports = %d, want 1: the commit after a squash must not re-report it", n)
	}
}

// TestLocalMergeIgnoresUnlandedBranchDuringSquash is the false-positive guard
// on the new probe: a branch with its own unlanded work must not be dragged
// in by somebody else's squash landing in the same commit range.
func TestLocalMergeIgnoresUnlandedBranchDuringSquash(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-3", "branch": "WL-3-two-commits", "state": "in_progress"},
		{"id": "WL-4", "branch": "WL-4-still-working", "state": "in_progress"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-4-still-working")
	r.commit("w.txt", "work in progress\n", "wip")
	r.git("checkout", "main")
	r.git("checkout", "-b", "WL-3-two-commits")
	r.commit("a.txt", "a\n", "first")
	r.commit("b.txt", "b\n", "second")
	r.git("checkout", "main")
	r.git("merge", "--squash", "WL-3-two-commits")
	r.git("commit", "-m", "squash WL-3")

	runGitHook(t, "post-commit", r.root)

	got := b.reported(t)
	if len(got) != 1 || got[0] != "WL-3" {
		t.Fatalf("reported %v, want [WL-3] only — WL-4 is still unlanded", got)
	}
}

// TestLocalMergeReportsSquashAfterMainMoved is the realistic GitHub flow: the
// branch forked, main gained unrelated commits, and only then was the branch
// squashed onto it. The branch's combined diff must be taken from where it
// forked, not from main's tip.
func TestLocalMergeReportsSquashAfterMainMoved(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-5", "branch": "WL-5-forked-early", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-5-forked-early")
	r.commit("a.txt", "a\n", "first")
	r.commit("b.txt", "b\n", "second")
	r.git("checkout", "main")
	r.commit("other.txt", "someone else\n", "unrelated work on main")
	r.git("merge", "--squash", "WL-5-forked-early")
	r.git("commit", "-m", "squash WL-5")

	runGitHook(t, "post-commit", r.root)

	if got := b.reported(t); len(got) != 1 || got[0] != "WL-5" {
		t.Fatalf("reported %v, want [WL-5]", got)
	}
}

// TestLocalMergeReportsSquashArrivingWithOtherCommits: a squash merged on the
// forge and then pulled arrives alongside whatever else landed, so the probe
// must search the whole range this event added, not just its tip.
func TestLocalMergeReportsSquashArrivingWithOtherCommits(t *testing.T) {
	b := newMergeBackbone(t, []map[string]any{
		{"id": "WL-6", "branch": "WL-6-arrives-in-a-batch", "state": "in_review"},
	})
	r := newMergeRepo(t)
	r.git("checkout", "-b", "WL-6-arrives-in-a-batch")
	r.commit("a.txt", "a\n", "first")
	r.commit("b.txt", "b\n", "second")
	// Stand in for the forge: squash the branch onto a line of work that then
	// arrives on main as a batch of commits, the squash not at its tip.
	r.git("checkout", "-b", "upstream", "main")
	r.git("merge", "--squash", "WL-6-arrives-in-a-batch")
	r.git("commit", "-m", "squash WL-6")
	r.commit("after.txt", "and then some\n", "a later upstream commit")
	r.git("checkout", "main")
	r.git("merge", "--no-ff", "-m", "Merge upstream", "upstream")

	runGitHook(t, "post-merge", r.root)

	if got := b.reported(t); len(got) != 1 || got[0] != "WL-6" {
		t.Fatalf("reported %v, want [WL-6]", got)
	}
}
