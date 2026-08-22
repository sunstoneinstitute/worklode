package hookrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/secrets"
)

func TestWorktreeRemoveReleasesLease(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Remove me")

	// tool_input carries the removed path; parse it out defensively.
	runHook(t, "worktree-remove", Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})
	if !rec.hitAny("/release") {
		t.Fatalf("release endpoint was not hit; paths: %v", rec.list())
	}
	detail, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease != nil {
		t.Fatalf("lease after worktree-remove = %+v, want nil", detail.Lease)
	}
}

// TestWorktreeRemovePurgesSecrets exercises the real-server path: a
// worktree-remove that also successfully releases the lease must purge the
// task's materialized secrets (spec 017: materialized lifetime equals
// worktree lifetime).
func TestWorktreeRemovePurgesSecrets(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Purge me")

	if err := secrets.Put(taskID, "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	runHook(t, "worktree-remove", Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})
	if !rec.hitAny("/release") {
		t.Fatalf("release endpoint was not hit; paths: %v", rec.list())
	}
	if _, err := secrets.Fetch(taskID, "A_TOKEN"); err == nil {
		t.Fatal("secret survived worktree removal")
	}
	if _, ok := secrets.LoadManifest(taskID); ok {
		t.Fatal("manifest survived worktree removal")
	}
}

// TestWorktreeRemovePurgesSecretsRegardlessOfBackbone proves the purge is
// unconditional on the backbone call's outcome: hooks never fail the event,
// and the local purge must happen even when there is no reachable backbone
// at all. Uses addWorktree (no real server needed) rather than
// setupLeasedWorktree so the worktree path parses under the layout
// (exactly one level below the base) without requiring a claim.
func TestWorktreeRemovePurgesSecretsRegardlessOfBackbone(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	const taskID = "WL-3"
	if err := secrets.Put(taskID, "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	root := initGitRepo(t)
	wtDir := addWorktree(t, root, taskID, "fix")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "worktree-remove",
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})),
		Stdout: &stdout,
		Stderr: &stderr,
		// No backbone: the release call will warn, but the local purge must
		// have happened regardless.
		NewClient: func() (*cli.Client, error) { return nil, errors.New("no config") },
	})
	if code != 0 {
		t.Fatalf("hook exit = %d; hooks never fail the event", code)
	}
	if _, err := secrets.Fetch(taskID, "A_TOKEN"); err == nil {
		t.Fatal("secret survived worktree removal")
	}
	if _, ok := secrets.LoadManifest(taskID); ok {
		t.Fatal("manifest survived worktree removal")
	}
}

// TestWorktreeExitKeepsSecrets is the converse of the two above, and the
// reason purgeSecrets is bound to removal rather than to a session leaving.
// A session that exits a worktree still holds that task's lease and can come
// back (spec 012 §4: one session working several tasks in sequence); spec 017
// §3 purges on exit only for a lease that is gone. Purging here would cost a
// fresh consent and a fresh Touch ID on return — unobtainable in the
// non-interactive session that is the common case.
func TestWorktreeExitKeepsSecrets(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())

	const taskID = "WL-4"
	if err := secrets.Put(taskID, "A_TOKEN", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := secrets.SaveManifest(secrets.Manifest{Task: taskID, Materialized: []string{"A_TOKEN"}}); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	root := initGitRepo(t)
	wtDir := addWorktree(t, root, taskID, "hop")
	if err := writeSessionMarker(wtDir, "sess-1", time.Now()); err != nil {
		t.Fatalf("write session marker: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:     "worktree-exit",
		Stdin:     bytes.NewReader(payloadJSON(t, Payload{Cwd: root, SessionID: "sess-1", ToolInput: pathToolInput(t, wtDir)})),
		Stdout:    &stdout,
		Stderr:    &stderr,
		NewClient: func() (*cli.Client, error) { return nil, errors.New("no config") },
	})
	if code != 0 {
		t.Fatalf("hook exit = %d; hooks never fail the event", code)
	}
	// Positive control: without it, "the secrets survived" would also be true
	// of a handler that returned at its first guard. The deliberately-failing
	// NewClient proves endSession was reached, and the marker removal proves
	// the handler ran to its end.
	if !strings.Contains(stderr.String(), "load config") {
		t.Fatalf("handler did not reach the backbone call; stderr: %q", stderr.String())
	}
	if _, ok := markerSessionID(wtDir); ok {
		t.Fatal("session marker survived worktree-exit")
	}
	if _, err := secrets.Fetch(taskID, "A_TOKEN"); err != nil {
		t.Fatalf("leaving the worktree purged a still-leased task's secrets: %v", err)
	}
	if _, ok := secrets.LoadManifest(taskID); !ok {
		t.Fatal("leaving the worktree removed a still-leased task's manifest")
	}
}

func TestWorktreeCreateAutoResumesExpiredLease(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Adopt me")

	// Sweep the lease so the task is back in ready with no lease.
	expireLease(t, st, taskID)
	detail, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.Lease != nil {
		t.Fatalf("precondition: lease should be swept, got %+v", detail.Lease)
	}

	runHook(t, "worktree-create", Payload{Cwd: root, ToolInput: pathToolInput(t, wtDir)})
	if !rec.hitAny("/claim") {
		t.Fatalf("claim endpoint was not hit for auto-resume; paths: %v", rec.list())
	}
	after, _, err := c.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after auto-resume: %v", err)
	}
	if after.State != "in_progress" || after.Lease == nil {
		t.Fatalf("task after auto-resume = state %q lease %+v, want in_progress/non-nil", after.State, after.Lease)
	}
}

// TestWorktreeExitWithoutExplicitPathIsNOP mirrors Claude Code's real
// ExitWorktree tool_input, which is {action, discard_changes} — no path key.
// handleWorktreeExit must not fall back to the payload cwd: by the time
// PostToolUse fires, ExitWorktree has already restored cwd to the worktree
// being returned TO, not the one being left, so a cwd fallback would end and
// delete the marker of the wrong worktree.
func TestWorktreeExitWithoutExplicitPathIsNOP(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "No explicit exit path")

	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	before, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session before exit: %v", err)
	}
	if before.EndedAt != nil {
		t.Fatal("precondition: session should be open before the exit attempt")
	}
	markerBefore, okBefore := readSessionMarker(wtDir)
	if !okBefore {
		t.Fatal("precondition: marker should exist after session-start")
	}
	beforeCount := rec.count("/agent-session")

	// The real ExitWorktree tool_input: no path key.
	toolInput, _ := json.Marshal(map[string]string{"action": "keep"})
	runHook(t, "worktree-exit", Payload{Cwd: wtDir, SessionID: "sess-1", ToolInput: toolInput})

	if rec.count("/agent-session") != beforeCount {
		t.Fatal("worktree-exit without an explicit path called the backbone")
	}
	markerAfter, okAfter := readSessionMarker(wtDir)
	if !okAfter || markerAfter != markerBefore {
		t.Fatalf("marker changed by a path-less worktree-exit: before=%+v after=%+v (ok=%v)",
			markerBefore, markerAfter, okAfter)
	}
	after, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session after exit: %v", err)
	}
	if after.EndedAt != nil {
		t.Fatal("session ended by a path-less worktree-exit")
	}
}

// TestWorktreeEnterExitSwitchesLease drives one session across two leased
// worktrees: entering the second opens a row under its lease with the same
// session id, and exiting stamps ended_at on that row while the first
// worktree's row stays open.
func TestWorktreeEnterExitSwitchesLease(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskA, wtDirA, _ := setupLeasedWorktree(t, c, root, "Task A")
	taskB, wtDirB, _ := setupLeasedWorktree(t, c, root, "Task B")

	// Start a session in the first worktree.
	runHook(t, "session-start", Payload{Cwd: wtDirA, SessionID: "sess-1"})

	leaseA, err := st.ActiveLease(t.Context(), taskA)
	if err != nil {
		t.Fatalf("active lease A: %v", err)
	}
	sessA, err := st.AgentSession(t.Context(), leaseA.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session A: %v", err)
	}
	if sessA.EndedAt != nil {
		t.Fatal("session A should still be open before entering B")
	}

	// Enter the second worktree: a new row opens under B's lease.
	toolInput := pathToolInput(t, wtDirB)
	runHook(t, "worktree-enter", Payload{Cwd: wtDirA, SessionID: "sess-1", ToolInput: toolInput})

	leaseB, err := st.ActiveLease(t.Context(), taskB)
	if err != nil {
		t.Fatalf("active lease B: %v", err)
	}
	sessB, err := st.AgentSession(t.Context(), leaseB.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session B: %v", err)
	}
	if sessB.EndedAt != nil {
		t.Fatal("session B should be open after worktree-enter")
	}

	// worktree-enter must also write B's marker — symmetric with
	// session-start — or heartbeats there debounce off forever and B looks
	// abandoned to offerScan/handleWorktreeCreate.
	if id, ok := markerSessionID(wtDirB); !ok || id != "sess-1" {
		t.Fatalf("markerSessionID(B) after enter = %q, %v, want sess-1, true", id, ok)
	}
	if !sessionMarkerFresh(wtDirB) {
		t.Fatal("worktree B marker not fresh after worktree-enter")
	}

	// A heartbeat past the debounce window in the entered worktree reaches
	// the backbone (proving the marker written above is actually usable).
	before := rec.count("/agent-session")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "heartbeat",
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: wtDirB, SessionID: "sess-1"})),
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    func() time.Time { return time.Now().Add(2 * time.Minute) },
	})
	if code != 0 {
		t.Fatalf("heartbeat in entered worktree exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if rec.count("/agent-session") != before+1 {
		t.Fatalf("heartbeat in entered worktree did not reach the backbone: count before=%d after=%d",
			before, rec.count("/agent-session"))
	}

	// Exit the second worktree: B's row closes, A's stays open.
	runHook(t, "worktree-exit", Payload{Cwd: wtDirA, SessionID: "sess-1", ToolInput: toolInput})

	sessB, err = st.AgentSession(t.Context(), leaseB.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session B after exit: %v", err)
	}
	if sessB.EndedAt == nil {
		t.Fatal("worktree-exit did not close session B")
	}

	sessA, err = st.AgentSession(t.Context(), leaseA.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session A after exit: %v", err)
	}
	if sessA.EndedAt != nil {
		t.Fatal("session A should remain open after exiting B")
	}

	// worktree-exit must remove B's marker — symmetric with session-end.
	if _, ok := markerSessionID(wtDirB); ok {
		t.Fatal("worktree B marker still present after worktree-exit")
	}
}
