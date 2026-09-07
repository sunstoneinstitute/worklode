package hookrun

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionMarkerHeartbeat(t *testing.T) {
	root := initGitRepo(t)
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	if err := writeSessionMarker(root, "sess-1", base); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	id, ok := markerSessionID(root)
	if !ok || id != "sess-1" {
		t.Fatalf("markerSessionID: got %q, %v", id, ok)
	}

	// writeSessionMarker leaves LastHeartbeatAt empty (only a heartbeat that
	// actually reached the backbone should stamp it), so a heartbeat is due
	// immediately, even moments after the marker was written.
	if !heartbeatDue(root, base.Add(1*time.Second)) {
		t.Fatal("heartbeat not due with no recorded heartbeat yet")
	}

	// Record the first heartbeat.
	if err := recordHeartbeat(root, base); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}

	// Within the debounce window of the recorded heartbeat: not due.
	if heartbeatDue(root, base.Add(30*time.Second)) {
		t.Fatal("heartbeat due 30s after the last one; want debounced")
	}
	// Past the window: due again.
	if !heartbeatDue(root, base.Add(2*time.Minute)) {
		t.Fatal("heartbeat not due 2m after the last one")
	}

	// Recording a heartbeat moves the window without disturbing the session id.
	if err := recordHeartbeat(root, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if heartbeatDue(root, base.Add(2*time.Minute+30*time.Second)) {
		t.Fatal("heartbeat due 30s after a recorded heartbeat; want debounced")
	}
	if id, ok := markerSessionID(root); !ok || id != "sess-1" {
		t.Fatalf("session id after heartbeat: got %q, %v", id, ok)
	}

	// No marker at all: nothing to heartbeat, and no session id.
	empty := initGitRepo(t)
	if heartbeatDue(empty, base) {
		t.Fatal("heartbeat due with no marker file")
	}
	if _, ok := markerSessionID(empty); ok {
		t.Fatal("markerSessionID found an id with no marker file")
	}
}

func TestHeartbeatReportsAgentSession(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Heartbeat task")

	// session-start opens the session and writes the marker.
	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if !rec.hitAny("/agent-session") {
		t.Fatal("session-start did not report the agent session")
	}

	// A heartbeat inside the debounce window makes no backbone call.
	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if rec.count("/agent-session") != before {
		t.Fatal("heartbeat inside the debounce window still called the backbone")
	}

	// The session is recorded against the task's lease.
	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	if sess.EndedAt != nil {
		t.Fatal("session should still be open")
	}

	// session-end closes it.
	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1"})
	sess, err = st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session after end: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("session-end did not close the session")
	}
}

func TestHeartbeatOutsideWorktreeIsNOP(t *testing.T) {
	rec := newRecordingServer(t)
	runHook(t, "heartbeat", Payload{Cwd: t.TempDir(), SessionID: "sess-1"})
	if rec.hit() {
		t.Fatal("heartbeat outside a Worklode worktree called the backbone")
	}
}

// TestHeartbeatSelfHealsMissingMarker: a worktree that has lost its marker
// (e.g. it was never written, or was deleted) must not go silent forever —
// heartbeatDue is false with no marker, so without self-healing nothing would
// ever create one again. A heartbeat carrying a session id in the payload
// writes the marker and reports immediately.
func TestHeartbeatSelfHealsMissingMarker(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Self heal")

	if _, ok := readSessionMarker(wtDir); ok {
		t.Fatal("precondition: no marker should exist yet")
	}

	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if rec.count("/agent-session") != before+1 {
		t.Fatal("heartbeat with no marker and a payload session id did not report")
	}
	id, ok := markerSessionID(wtDir)
	if !ok || id != "sess-1" {
		t.Fatalf("marker not self-healed: id=%q ok=%v", id, ok)
	}
}

// TestHeartbeatUpdatesStaleMarkerID: when the payload's session id differs
// from the one recorded in the marker (e.g. after a /clear starts a new
// session in the same worktree), the marker must be brought up to date —
// otherwise a later marker-only report (pre-commit) would keep reporting the
// stale, no-longer-live session.
func TestHeartbeatUpdatesStaleMarkerID(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Stale marker id")

	runHook(t, "session-start", Payload{Cwd: wtDir, SessionID: "sess-old"})

	before := rec.count("/agent-session")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-new"})
	if rec.count("/agent-session") != before+1 {
		t.Fatal("heartbeat with a differing session id did not report")
	}
	id, ok := markerSessionID(wtDir)
	if !ok || id != "sess-new" {
		t.Fatalf("marker id after drift = %q, %v, want sess-new, true", id, ok)
	}
}

func writeProjectConfig(t *testing.T, dir, project string) {
	t.Helper()
	confDir := filepath.Join(dir, ".worklode")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", confDir, err)
	}
	content := fmt.Sprintf("current_project = %q\n", project)
	if err := os.WriteFile(filepath.Join(confDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
}

// TestHeartbeatDoesNotReportUsage pins the cutover (WL-658): live token
// accounting moved to Edge Agent telemetry, so a heartbeat is lifecycle only.
// The same hook run proves both halves — the transcript the harness still
// sends produces no /session-usage traffic, and the session row is still
// touched.
func TestHeartbeatDoesNotReportUsage(t *testing.T) {
	_, client, rec := newRealServer(t)
	root := initGitRepo(t)
	writeProjectConfig(t, root, "proj")
	_, wtDir, _ := setupLeasedWorktree(t, client, root, "lifecycle-only")
	path := writeTranscript(t,
		transcriptLine(wtDir, "msg-1", "claude-opus-5", 10, 0, 0, 0, 2),
	)

	beforeUsage := rec.count("/session-usage")
	beforeLifecycle := rec.count("/agent-session")
	runHookRaw(t, "heartbeat", map[string]any{
		"cwd": wtDir, "session_id": "sess-1", "transcript_path": path,
	})
	if got := rec.count("/session-usage"); got != beforeUsage {
		t.Fatalf("session-usage calls = %d, want %d", got, beforeUsage)
	}
	if got := rec.count("/agent-session"); got <= beforeLifecycle {
		t.Fatal("heartbeat stopped touching the lifecycle session")
	}
}
