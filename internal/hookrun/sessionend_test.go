package hookrun

import (
	"testing"
	"time"
)

func TestSessionEndRemovesMarker(t *testing.T) {
	_, c, _ := newRealServer(t)
	root := initGitRepo(t)
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "End me")

	if err := writeSessionMarker(wtDir, "s-end", time.Now()); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if !sessionMarkerFresh(wtDir) {
		t.Fatalf("precondition: marker should be fresh")
	}

	runHook(t, "session-end", Payload{Cwd: wtDir})
	if sessionMarkerFresh(wtDir) {
		t.Fatalf("session marker still present after session-end")
	}
}

// TestSessionEndIsLifecycleOnly pins the cutover (WL-658): ending a session
// closes its row and nothing more. The harness still sends transcript_path,
// and the project usage endpoint is never called for it.
func TestSessionEndIsLifecycleOnly(t *testing.T) {
	_, c, rec := newRealServer(t)
	root := initGitRepo(t)
	writeProjectConfig(t, root, "proj")
	_, wtDir, _ := setupLeasedWorktree(t, c, root, "lifecycle-only")
	path := writeTranscript(t,
		transcriptLine(wtDir, "msg-1", "claude-opus-5", 10, 0, 0, 0, 2),
	)

	beforeUsage := rec.count("/session-usage")
	runHookRaw(t, "session-end", map[string]any{
		"cwd": wtDir, "session_id": "sess-1", "transcript_path": path,
	})

	if got := rec.count("/session-usage"); got != beforeUsage {
		t.Fatalf("session-usage calls = %d, want %d", got, beforeUsage)
	}
	if rec.count("/agent-session/end") == 0 {
		t.Fatalf("session-end did not close the session: %v", rec.list())
	}
}

// The end request itself carries no usage, so nothing this hook sends can
// overwrite the totals the live source records.
func TestSessionEndRequestCarriesNoUsage(t *testing.T) {
	rec := newEndRecorder(t)
	root := initGitRepo(t)
	wtDir := addWorktree(t, root, "WL-2", "lifecycle-only")

	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1"})
	if got := string(rec.only(t)["usage"]); got != "null" {
		t.Fatalf("usage = %s, want null (nil must leave stored usage alone)", got)
	}
}
