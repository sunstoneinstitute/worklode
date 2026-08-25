package hookrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

func TestPreCommitRenewsInsideWorktree(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Renew me")

	before, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	runHook(t, "pre-commit", Payload{Cwd: wtDir, HookEventName: "PreToolUse"})
	if !rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was not hit; paths: %v", rec.list())
	}
	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after renew: %v", err)
	}
	if after.Lease == nil || before.Lease == nil {
		t.Fatalf("lease missing: before=%+v after=%+v", before.Lease, after.Lease)
	}
	if after.Lease.RenewedAt.Before(before.Lease.RenewedAt) {
		t.Fatalf("renewed_at went backwards: %v -> %v", before.Lease.RenewedAt, after.Lease.RenewedAt)
	}
}

// TestPreCommitWithoutLeaseIsSilent: committing in a worktree that holds no
// lease — swept, released, or never claimed — is ordinary. The hook must not
// renew, must not report a session (both would 404), must not warn, and must
// not block the commit.
func TestPreCommitWithoutLeaseIsSilent(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "No lease here")
	if _, err := c.ReleaseLease(context.Background(), taskID); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if err := writeSessionMarker(wtDir, "s-nolease", time.Time{}); err != nil {
		t.Fatalf("write marker: %v", err) // a zero heartbeat makes one due
	}

	_, stderr := runHookOutput(t, "pre-commit", Payload{Cwd: wtDir, HookEventName: "PreToolUse"})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (a missing lease is not a warning)", stderr)
	}
	if rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was hit with no lease held; paths: %v", rec.list())
	}
	if rec.hitAny("/agent-session") {
		t.Fatalf("agent-session endpoint was hit with no lease held; paths: %v", rec.list())
	}
}

// TestPreCommitResolvesTaskIDFromGitConfigAfterWorktreeRename covers the case
// the explicit worklode.task-id field exists for: a worktree renamed to a
// directory name that carries no task id. It still sits one level below the
// base, so it clears spec 008 §5.2's guard; only the stamped git config can
// then say which task it belongs to.
func TestPreCommitResolvesTaskIDFromGitConfigAfterWorktreeRename(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Explicit id")

	if err := worktree.EnableWorktreeConfigExtension(root); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	if err := worktree.SetTaskID(wtDir, taskID); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}

	renamed := filepath.Join(root, worktree.DefaultBase, "no-id-in-this-name")
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, renamed).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	// Guard the guard: if the new name still carried an id, the fallback
	// would resolve it and the test would prove nothing about the config.
	if id, ok := testLayout(t).ParseDir(renamed); ok {
		t.Fatalf("ParseDir(%s) = (%q, true), want ok=false — the renamed directory must not carry an id", renamed, id)
	}

	_, stderr := runHookOutput(t, "pre-commit", Payload{Cwd: renamed, HookEventName: "PreToolUse"})
	// Reaching the brief is the assertion: the handler only gets that far
	// once the task id resolved, and the directory name can no longer supply
	// it.
	if !rec.hitAny("/tasks/" + taskID + "/brief") {
		t.Fatalf("brief for %s was not fetched, so the task id did not resolve; paths: %v", taskID, rec.list())
	}
	// The lease is not renewed, and that is correct rather than a gap here:
	// lease ownership is keyed on worktree.Identity, which is the worktree's
	// absolute path, so a moved worktree no longer holds the lease it claimed.
	// Resolving the task id and owning the lease are separate questions.
	if rec.hitAny("/renew") {
		t.Fatalf("renew endpoint was hit for a lease bound to the pre-move path; paths: %v", rec.list())
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (a lease bound elsewhere is not a warning)", stderr)
	}
}

// TestPreCommitIgnoresStampedWorktreeOutsideTheBase pins the other half of
// §3.2's split: the guard is a pure string question, and a stamped worktree
// that fails it stays invisible to Worklode. Without this, the git-config
// lookup would quietly widen the guard the spec deliberately keeps cheap.
func TestPreCommitIgnoresStampedWorktreeOutsideTheBase(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Outside the base")

	if err := worktree.EnableWorktreeConfigExtension(root); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	if err := worktree.SetTaskID(wtDir, taskID); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}

	outside := filepath.Join(root, "elsewhere", taskID+"-outside-the-base")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}

	rec.reset()
	_, stderr := runHookOutput(t, "pre-commit", Payload{Cwd: outside, HookEventName: "PreToolUse"})
	if paths := rec.list(); len(paths) != 0 {
		t.Fatalf("backbone was called for a path outside %s; paths: %v", worktree.DefaultBase, paths)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty (a path outside the base is a silent NOP)", stderr)
	}
}
