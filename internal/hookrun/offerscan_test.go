package hookrun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// offerScan runs when session-start fires OUTSIDE a worktree. A worktree whose
// lease has expired and whose marker is absent is offered for adoption.
func TestOfferScanOffersAbandonedWorktree(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Abandoned worktree")
	expireLease(t, st, taskID)

	ctx := offerScanContext(t, root)
	if !strings.Contains(ctx, ".worktrees/"+filepath.Base(wtDir)) {
		t.Fatalf("additionalContext does not offer the worktree: %q", ctx)
	}
	if !strings.Contains(ctx, taskID) {
		t.Fatalf("additionalContext missing task id %q: %q", taskID, ctx)
	}
}

// The layout is flat (spec 008 §5.1): offerScan reads one level below the base
// and nothing deeper, so a worktree re-homed into a subdirectory is not a
// worktree root any more and is not offered. This pins the flat scan — the
// pre-flat code walked to depth 3 and would have found it.
func TestOfferScanIgnoresNestedWorktree(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Nested worktree")

	nested := filepath.Join(root, ".worktrees", "team", filepath.Base(wtDir))
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, nested).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	expireLease(t, st, taskID)

	if ctx := offerScanContext(t, root); ctx != "" {
		t.Fatalf("nested worktree was offered for adoption: %q", ctx)
	}
}

// offerScan only ever walks the configured base directory (hookrun.go's
// offerScan), so this proves that narrower property — the scan never reaches
// outside its base — not that ParseDir has stopped accepting wt/: the scan
// would skip anything under wt/ even if ParseDir still recognised it. The
// genuine "legacy wt/ is gone" coverage (spec 008 §7) is
// TestLayoutParseDir's "legacy wt is gone" case in
// internal/worktree/worktree_test.go.
func TestOfferScanIgnoresLegacyWtDir(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Legacy worktree")

	legacy := filepath.Join(root, "wt", filepath.Base(wtDir))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, legacy).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	expireLease(t, st, taskID)

	if ctx := offerScanContext(t, root); ctx != "" {
		t.Fatalf("legacy wt/ worktree was offered for adoption: %q", ctx)
	}
}

// A non-default worktree_dir (here via LODE_WORKTREE_DIR, the env override
// spec 008 §5.1 gives) must be honoured, not just tolerated: the guard has to
// find a worktree that isn't under the default .worktrees at all.
func TestLayoutCustomBaseHonored(t *testing.T) {
	rec := newRecordingServer(t)
	root := initGitRepo(t)
	t.Setenv("LODE_WORKTREE_DIR", "custom-base")
	wtDir := addWorktreeAt(t, root, "custom-base", "PROJ-1", "custom")

	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "s-custom-base"})
	if !rec.hit() {
		t.Fatalf("guard did not fire for a worktree under the configured custom base dir")
	}
}

// A malformed worktree_dir (NewLayout rejects an absolute path) must degrade
// to the default .worktrees layout with a warning, never fail the event or
// leave the guard permanently blind.
func TestLayoutMalformedWorktreeDirDegradesToDefault(t *testing.T) {
	rec := newRecordingServer(t)
	root := initGitRepo(t)
	t.Setenv("LODE_WORKTREE_DIR", "/not/relative/to/the/repo")
	wtDir := addWorktree(t, root, "PROJ-1", "degrade")

	_, stderr := runHookOutput(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "s-degrade"})
	if !rec.hit() {
		t.Fatalf("malformed worktree_dir should degrade to the default .worktrees layout, not NOP (stderr: %s)", stderr)
	}
	if !strings.Contains(stderr, "resolve worktree layout") {
		t.Fatalf("expected a warning about the malformed worktree_dir, got stderr=%q", stderr)
	}
}
