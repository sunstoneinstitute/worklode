package hookrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
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

// TestHeartbeatPostsTranscriptUsage covers the only path a crashed or swept
// session's spend can reach the backbone by: usage rides the heartbeat, and
// carries the same per-worktree filtering the clean end applies. A heartbeat
// with no transcript posts no usage field at all, so it leaves whatever the
// backbone already recorded alone rather than clearing it.
func TestHeartbeatPostsTranscriptUsage(t *testing.T) {
	rec := newUsageRecorder(t)
	root := initGitRepo(t)
	writeProjectConfig(t, root, "proj")
	wtDir := addWorktree(t, root, "WL-1", "bill-me")
	elsewhere := t.TempDir()

	path := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-opus-5", 100, 200, 300, 400, 50),
		transcriptLine(elsewhere, "msg_2", "claude-opus-5", 9_000, 9_000, 9_000, 9_000, 9_000),
	)

	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: path})

	byTask := rec.byTask(t)
	want := model.SessionUsageBucket{
		Day: "2026-07-31", Model: "claude-opus-5", Speed: "standard",
		InputTokens: 100, CacheWrite5mTokens: 200, CacheWrite1hTokens: 300,
		CacheReadTokens: 400, OutputTokens: 50,
	}
	if own := byTask["WL-1"]; len(own) != 1 || own[0] != want {
		t.Fatalf("WL-1 usage = %+v, want %+v", own, want)
	}

	// A heartbeat with no transcript has nothing to classify, so it reports
	// no usage at all rather than posting an empty classification, which
	// would clear what the backbone already recorded.
	quiet := addWorktree(t, root, "WL-2", "no-transcript")
	runHook(t, "heartbeat", Payload{Cwd: quiet, SessionID: "sess-2"})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 1 {
		t.Fatalf("session-usage called %d times, want 1 — the transcript-less heartbeat must not report", len(rec.bodies))
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

// TestHeartbeatFromMainCheckoutSplitsTaskAndOverhead is the bug this plan
// exists to fix: an orchestrator session runs from the repo's main checkout
// and dispatches a subagent into a task worktree, but Claude Code logs the
// subagent's turns into the orchestrator's OWN transcript, tagged with the
// directory each ran in. A heartbeat fired from the main checkout must still
// split that transcript's usage correctly: the cwd under a currently
// lease-held task worktree bills to that task, and the cwd with no task at all
// (the main checkout itself) bills to project overhead — neither is dropped,
// and one /session-usage call carries both.
func TestHeartbeatFromMainCheckoutSplitsTaskAndOverhead(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Main checkout task")
	writeProjectConfig(t, root, "proj")

	transcriptPath := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-sonnet-5", 100, 0, 0, 0, 10),
		transcriptLine(root, "msg_2", "claude-sonnet-5", 50, 0, 0, 0, 5),
	)

	beforeUsage := rec.count("/session-usage")
	runHook(t, "heartbeat", Payload{Cwd: root, SessionID: "sess-orch", TranscriptPath: transcriptPath})

	if rec.count("/session-usage") != beforeUsage+1 {
		t.Fatal("main-checkout heartbeat did not report the session's usage")
	}

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-orch")
	if err != nil {
		t.Fatalf("agent session for the worktree's task: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 100 {
		t.Fatalf("worktree task input tokens = %v, want 100", sess.InputTokens)
	}

	// The main checkout's own turns have no task to bill to and land in the
	// project's overhead bucket, and the project's combined cost counts each
	// side once.
	report, err := st.ProjectCost(t.Context(), "proj", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d cost days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadTokens.Input; got != 50 {
		t.Errorf("overhead input = %d, want 50 (the main checkout's own turn)", got)
	}
	if got := report.Days[0].Tokens.Input; got != 150 {
		t.Errorf("combined input = %d, want 150 (100 task + 50 overhead, each counted once)", got)
	}
}

// Classifying usage per cwd must not move a task's own tokens to overhead.
// A recorded cwd is routinely a directory *inside* the worktree rather than
// its root, and an older transcript records no cwd at all; both belong to the
// task the hook is running for. Billing either to project overhead would take
// real money off the task's cost report (spec 052 §3).
func TestHeartbeatBillsSubdirAndCwdlessTurnsToItsOwnTask(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Own task")
	writeProjectConfig(t, wtDir, "proj")

	transcriptPath := writeTranscript(t,
		transcriptLine(filepath.Join(wtDir, "internal", "store"), "msg_1", "claude-sonnet-5", 100, 0, 0, 0, 10),
		transcriptLine("", "msg_2", "claude-sonnet-5", 50, 0, 0, 0, 5),
	)

	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: transcriptPath})

	report, err := st.ProjectCost(t.Context(), "proj", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d cost days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadTokens.Input; got != 0 {
		t.Errorf("overhead input = %d, want 0; the task's own turns must not become overhead", got)
	}

	lease, err := st.ActiveLease(t.Context(), taskID)
	if err != nil {
		t.Fatalf("active lease: %v", err)
	}
	sess, err := st.AgentSession(t.Context(), lease.ID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 150 {
		t.Fatalf("task input tokens = %v, want 150 (the subdirectory turn plus the cwd-less one)", sess.InputTokens)
	}
}

// TestHeartbeatDropsAnotherRepositorysWorktree pins the containment rule.
// The worktree layout is matched on path segments alone, so a cwd under any
// directory named `.worktrees` resolves to a task id — including one in a
// completely different repository. Those tokens belong to that repo's project
// and must not land on this one's overhead, where nothing would ever remove
// them (WL-329).
func TestHeartbeatDropsAnotherRepositorysWorktree(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Own task")
	writeProjectConfig(t, wtDir, "proj")

	// A second repository, with a worktree of its own. Its task id (TH-9)
	// matches the same pattern this repo's does.
	foreign := addWorktree(t, initGitRepo(t), "TH-9", "someone-elses-task")

	transcriptPath := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-sonnet-5", 100, 0, 0, 0, 10),
		transcriptLine(foreign, "msg_2", "claude-sonnet-5", 50, 0, 0, 0, 5),
	)

	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: transcriptPath})

	report, err := st.ProjectCost(t.Context(), "proj", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d cost days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadTokens.Input; got != 0 {
		t.Errorf("overhead input = %d, want 0 — another repo's worktree is not this project's overhead", got)
	}
	if got := report.Days[0].Tokens.Input; got != 100 {
		t.Errorf("combined input = %d, want 100 (this task's turn only)", got)
	}
}

// TestHeartbeatOtherTaskWithoutLeaseFallsBackToOverhead is the trap this
// plan warns about most: a transcript names a task this actor no longer
// holds the lease on (released, swept, or claimed by someone else), so
// TouchAgentSession fails (typically ErrNotFound / 404). Those tokens must
// still be reported — as project overhead — rather than silently dropped,
// which would reintroduce the very bug this plan fixes in a new place.
func TestHeartbeatOtherTaskWithoutLeaseFallsBackToOverhead(t *testing.T) {
	st, c, rec := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "Own task")
	otherTaskID, otherWtDir, _ := setupLeasedWorktree(t, c, root, "No longer held")

	if _, err := c.ReleaseLease(context.Background(), otherTaskID); err != nil {
		t.Fatalf("release other task's lease: %v", err)
	}
	writeProjectConfig(t, wtDir, "proj")

	transcriptPath := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-sonnet-5", 100, 0, 0, 0, 10),
		transcriptLine(otherWtDir, "msg_2", "claude-sonnet-5", 50, 0, 0, 0, 5),
	)

	beforeUsage := rec.count("/session-usage")
	runHook(t, "heartbeat", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: transcriptPath})

	if rec.count("/session-usage") != beforeUsage+1 {
		t.Fatal("heartbeat did not report the session's usage")
	}

	// The unleased task has no session to bill, so the server puts its tokens
	// in overhead rather than dropping them — and counts them once.
	report, err := st.ProjectCost(t.Context(), "proj", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ProjectCost: %v", err)
	}
	if len(report.Days) != 1 {
		t.Fatalf("got %d cost days, want 1: %+v", len(report.Days), report.Days)
	}
	if got := report.Days[0].OverheadTokens.Input; got != 50 {
		t.Errorf("overhead input = %d, want 50 — the unleased task's tokens are not dropped", got)
	}
	if got := report.Days[0].Tokens.Input; got != 150 {
		t.Errorf("combined input = %d, want 150 (100 own task + 50 overhead, each once)", got)
	}
}
