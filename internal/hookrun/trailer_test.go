package hookrun

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// taskWorktree is a git repo shaped like a Worklode worktree — a real repo at
// <tmp>/.worktrees/<id>-<slug> — so Layout.TaskID resolves an id from it the
// way it does in the field.
type taskWorktree struct {
	t    *testing.T
	root string
}

func newTaskWorktree(t *testing.T, dirName string) *taskWorktree {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".worktrees", dirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	w := &taskWorktree{t: t, root: root}
	w.git("-c", "init.defaultBranch=main", "init")
	w.commit("README.md", "hello\n", "initial commit")
	return w
}

func (w *taskWorktree) git(args ...string) string {
	w.t.Helper()
	c := exec.Command("git", append([]string{"-C", w.root, "-c", "commit.gpgsign=false"}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := c.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *taskWorktree) commit(name, content, msg string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.root, name), []byte(content), 0o644); err != nil {
		w.t.Fatalf("write %s: %v", name, err)
	}
	w.git("add", name)
	w.git("commit", "-m", msg)
}

// msg writes a commit message file and returns the path git would pass as $1:
// relative to the top of the working tree, which is where git runs a hook.
func (w *taskWorktree) msg(content string) string {
	w.t.Helper()
	rel := filepath.Join(".git", "COMMIT_EDITMSG")
	if err := os.WriteFile(filepath.Join(w.root, rel), []byte(content), 0o644); err != nil {
		w.t.Fatalf("write commit message: %v", err)
	}
	return rel
}

func (w *taskWorktree) readMsg(rel string) string {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.root, rel))
	if err != nil {
		w.t.Fatalf("read commit message: %v", err)
	}
	return string(b)
}

// runCommitMsg drives the commit-msg event the way the installed hook does:
// git's $1 in Args, the working directory on stdin.
func runCommitMsg(t *testing.T, w *taskWorktree, msgFile string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "commit-msg",
		Args:   []string{msgFile},
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: w.root})),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("commit-msg exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	return stderr.String()
}

func TestCommitMsgStampsTrailer(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	f := w.msg("Fix the thing\n\nA paragraph of body text.\n")

	runCommitMsg(t, w, f)

	want := "Fix the thing\n\nA paragraph of body text.\n\nWorklode-Task: WL-88\n"
	if got := w.readMsg(f); got != want {
		t.Fatalf("message =\n%q\nwant\n%q", got, want)
	}
}

// TestCommitMsgJoinsExistingTrailerBlock: the trailer has to land inside the
// trailer block, not as a new paragraph — TaskIDFromBody anchors on the line.
func TestCommitMsgJoinsExistingTrailerBlock(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	f := w.msg("Subject\n\nBody.\n\nReviewed-by: someone\n")

	runCommitMsg(t, w, f)

	want := "Subject\n\nBody.\n\nReviewed-by: someone\nWorklode-Task: WL-88\n"
	if got := w.readMsg(f); got != want {
		t.Fatalf("message =\n%q\nwant\n%q", got, want)
	}
}

// TestCommitMsgIsIdempotent: the event fires again on every `git commit
// --amend` and on every commit a rebase replays.
func TestCommitMsgIsIdempotent(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	f := w.msg("Subject\n\nBody.\n")

	runCommitMsg(t, w, f)
	first := w.readMsg(f)
	runCommitMsg(t, w, f)
	runCommitMsg(t, w, f)

	if got := w.readMsg(f); got != first {
		t.Fatalf("message drifted on replay:\n%q\nwant\n%q", got, first)
	}
	if n := strings.Count(w.readMsg(f), trailerToken); n != 1 {
		t.Fatalf("%s appears %d times, want 1", trailerToken, n)
	}
}

// TestCommitMsgLeavesForeignTrailerAlone: a cherry-picked commit keeps the
// attribution it arrived with rather than gaining a second, contradictory one.
func TestCommitMsgLeavesForeignTrailerAlone(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	before := "Subject\n\nBody.\n\nWorklode-Task: SW-3\n"
	f := w.msg(before)

	runCommitMsg(t, w, f)

	if got := w.readMsg(f); got != before {
		t.Fatalf("message =\n%q\nwant it unchanged:\n%q", got, before)
	}
}

// TestCommitMsgSkipsCommentsOnlyMessage is the abort path: git runs commit-msg
// BEFORE it decides the message is empty, so stamping a comments-only message
// would give it a body and commit what the author meant to throw away.
func TestCommitMsgSkipsCommentsOnlyMessage(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	before := "\n# Please enter the commit message for your changes.\n# On branch main\n"
	f := w.msg(before)

	runCommitMsg(t, w, f)

	if got := w.readMsg(f); got != before {
		t.Fatalf("message =\n%q\nwant it unchanged:\n%q", got, before)
	}
}

// TestCommitMsgSkipsMerge: a merge commit's message is git's, not the
// author's, and the merge is not this task's work.
func TestCommitMsgSkipsMerge(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	w.git("checkout", "-b", "side")
	w.commit("side.txt", "side\n", "side work")
	w.git("checkout", "main")
	w.commit("main.txt", "main\n", "main work")
	// --no-commit leaves MERGE_HEAD in place, which is the state the hook
	// sees when git is about to write the merge commit.
	w.git("merge", "--no-commit", "--no-ff", "side")

	before := "Merge branch 'side'\n"
	f := w.msg(before)
	runCommitMsg(t, w, f)

	if got := w.readMsg(f); got != before {
		t.Fatalf("message =\n%q\nwant it unchanged during a merge:\n%q", got, before)
	}
}

// TestCommitMsgSkipsOutsideTaskWorktree: committing in the main clone is the
// common case and must leave the message alone.
func TestCommitMsgSkipsOutsideTaskWorktree(t *testing.T) {
	root := initGitRepo(t) // a plain repo, not under .worktrees/
	before := "Subject\n\nBody.\n"
	rel := filepath.Join(".git", "COMMIT_EDITMSG")
	if err := os.WriteFile(filepath.Join(root, rel), []byte(before), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	var stdout, stderr bytes.Buffer
	Run(context.Background(), Options{
		Event:  "commit-msg",
		Args:   []string{rel},
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: root})),
		Stdout: &stdout,
		Stderr: &stderr,
	})

	got, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read commit message: %v", err)
	}
	if string(got) != before {
		t.Fatalf("message =\n%q\nwant it unchanged outside a task worktree", got)
	}
}

// TestCommitMsgPrefersStampedTaskID: the explicit worklode.task-id config wins
// over the id in the directory name, so a renamed worktree still stamps right.
func TestCommitMsgPrefersStampedTaskID(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	w.git("config", "extensions.worktreeConfig", "true")
	w.git("config", "--worktree", "worklode.task-id", "SW-3")
	f := w.msg("Subject\n\nBody.\n")

	runCommitMsg(t, w, f)

	if got := w.readMsg(f); !strings.Contains(got, "Worklode-Task: SW-3") {
		t.Fatalf("message =\n%q\nwant the stamped id SW-3", got)
	}
}

// TestCommitMsgWithoutMessageFileWarns: nothing to stamp, and still no failure.
func TestCommitMsgWithoutMessageFileWarns(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "commit-msg",
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: w.root})),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "no message file") {
		t.Fatalf("stderr = %q, want a missing-argument warning", stderr.String())
	}
}

// TestCommitMsgTrailerIsReadableBack closes the loop the whole task exists
// for: what the hook writes is what store.TaskIDFromBody's ^Worklode-Task:
// pattern reads. Restated here rather than imported — internal/hookrun must
// not depend on internal/store — and held to the same anchor.
func TestCommitMsgTrailerIsReadableBack(t *testing.T) {
	w := newTaskWorktree(t, "WL-88-stamp-the-trailer")
	f := w.msg("Subject\n\nBody.\n\nReviewed-by: someone\n")
	runCommitMsg(t, w, f)

	var found string
	for _, line := range strings.Split(w.readMsg(f), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), trailerToken+": "); ok {
			found = rest
			break
		}
	}
	if found != "WL-88" {
		t.Fatalf("trailer read back as %q, want WL-88", found)
	}
}
