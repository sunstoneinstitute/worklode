package hookrun

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mergeRepo is a throwaway clone-shaped repo: a default branch, an origin
// remote, and a git runner that keeps the developer's global config out of it.
type mergeRepo struct {
	t    *testing.T
	root string
}

func newMergeRepo(t *testing.T) *mergeRepo {
	t.Helper()
	r := &mergeRepo{t: t, root: t.TempDir()}
	r.git("-c", "init.defaultBranch=main", "init")
	r.git("remote", "add", "origin", "git@github.com:acme/app.git")
	r.commit("README.md", "hello\n", "initial commit")
	return r
}

func (r *mergeRepo) git(args ...string) string {
	r.t.Helper()
	c := exec.Command("git", append([]string{"-C", r.root, "-c", "commit.gpgsign=false"}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := c.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *mergeRepo) commit(name, content, msg string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.root, name), []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", name, err)
	}
	r.git("add", name)
	r.git("commit", "-m", msg)
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
