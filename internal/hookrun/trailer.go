package hookrun

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// trailerToken is the label of the commit trailer that binds a commit to a
// task (spec 004 §2.4). store.TaskIDFromBody reads it back off the message;
// this is the only thing that writes it.
const trailerToken = "Worklode-Task"

// handleCommitMsg stamps `Worklode-Task: <id>` into the message of a commit
// made inside a task worktree, so the commit carries its own correlation
// rather than depending on a branch name, a PR, or a merge event — every one
// of which is attached to something outside the commit and is lost when the
// work is rebased, cherry-picked, or squashed.
//
// commit-msg rather than prepare-commit-msg, which the two hooks' timing
// decides. Both run before git's empty-message check, so a hook that stamps
// unconditionally turns "quit the editor without writing a message" from an
// abort into a commit whose entire message is the trailer. The gate against
// that is to stamp only a message that already has a body — and at
// prepare-commit-msg time an interactive commit's body is always empty,
// because the author has not written it yet. commit-msg sees the finished
// message, so the same gate keeps the abort and still stamps the interactive
// case.
//
// Same contract as pre-commit: every failure is a warning, and nothing here
// can fail the commit.
func handleCommitMsg(opts Options, dir string, l worktree.Layout) {
	if len(opts.Args) == 0 {
		warn(opts, "commit-msg: no message file argument (commit not blocked)")
		return
	}
	root, taskID, ok := leasedWorktree(l, dir)
	if !ok {
		return // outside a repo, or not a task worktree: the main clone commits unstamped
	}
	// git runs a hook from the top of the working tree, so a relative message
	// path is relative to root.
	msgFile := opts.Args[0]
	if !filepath.IsAbs(msgFile) {
		msgFile = filepath.Join(root, msgFile)
	}
	if gitexec.OK(root, "rev-parse", "--verify", "--quiet", "MERGE_HEAD") {
		// A merge commit's message is git's, not the author's, and the merge
		// itself is not this task's work.
		return
	}
	body, err := hasMessageBody(root, msgFile)
	if err != nil {
		warn(opts, "read commit message %s: %v (commit not blocked)", msgFile, err)
		return
	}
	if !body {
		// Comments only: the author is aborting. Stamping would give the
		// message a body and commit what they meant to throw away.
		return
	}
	if err := stampTrailer(root, msgFile, taskID); err != nil {
		warn(opts, "stamp %s trailer for %s: %v (commit not blocked)", trailerToken, taskID, err)
	}
}

// hasMessageBody reports whether the message file holds anything but comments
// and blank lines — the same emptiness test git itself applies after the hook
// returns, run through git so it honors core.commentChar.
func hasMessageBody(root, msgFile string) (bool, error) {
	f, err := os.Open(msgFile) //nolint:gosec // path comes from git, via the hook argv
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	cmd := gitexec.Cmd(root, "stripspace", "--strip-comments")
	cmd.Stdin = f
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git stripspace: %w", err)
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// stampTrailer adds the trailer to the message in place.
//
// `git interpret-trailers` rather than an append, because the trailer has to
// land inside the message's trailer block for TaskIDFromBody's line-anchored
// pattern to find it: after the body, beside any other trailer, with the blank
// line before it that a message without trailers does not yet have.
//
// --if-exists doNothing makes this idempotent, which it has to be: commit-msg
// fires again on every `git commit --amend` and on every commit a rebase
// replays. It leaves a trailer carrying some other task's id alone too, so a
// cherry-picked commit keeps the attribution it arrived with rather than
// gaining a second, contradictory one.
func stampTrailer(root, msgFile, taskID string) error {
	return gitexec.Run(root, "interpret-trailers",
		"--in-place", "--if-exists", "doNothing",
		"--trailer", trailerToken+": "+taskID, msgFile)
}
