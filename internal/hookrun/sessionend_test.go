package hookrun

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
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

// TestSessionEndPostsTranscriptUsage drives the whole accounting path: the
// same message id repeated across content-block lines is billed once, and a
// turn that ran in a different directory belongs to that worktree's lease, not
// this one.
func TestSessionEndPostsTranscriptUsage(t *testing.T) {
	rec := newUsageRecorder(t)
	root := initGitRepo(t)
	writeProjectConfig(t, root, "proj")
	wtDir := addWorktree(t, root, "WL-1", "bill-me")
	elsewhere := t.TempDir()

	path := writeTranscript(t,
		transcriptLine(wtDir, "msg_1", "claude-opus-5", 100, 200, 300, 400, 50),
		transcriptLine(wtDir, "msg_1", "claude-opus-5", 100, 200, 300, 400, 50), // same message, second content block
		transcriptLine(elsewhere, "msg_2", "claude-opus-5", 9_000, 9_000, 9_000, 9_000, 9_000),
	)

	runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: path})

	byTask := rec.byTask(t)
	want := model.SessionUsageBucket{
		Day: "2026-07-31", Model: "claude-opus-5", Speed: "standard",
		InputTokens: 100, CacheWrite5mTokens: 200, CacheWrite1hTokens: 300,
		CacheReadTokens: 400, OutputTokens: 50,
	}
	own := byTask["WL-1"]
	if len(own) != 1 || own[0] != want {
		t.Fatalf("WL-1 usage = %+v, want %+v", own, want)
	}
	// The turn that ran outside any worktree is classified, not dropped: it
	// belongs to no task, so it reports under the overhead key.
	other := byTask[""]
	if len(other) != 1 || other[0].InputTokens != 9_000 {
		t.Fatalf("overhead usage = %+v, want the 9000-token turn from elsewhere", other)
	}
}

// A hook must never fail its triggering event, so an unreadable transcript
// still ends the session — just with no usage attached.
func TestSessionEndWithoutTranscriptStillEndsSession(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"absent field", ""},
		{"missing file", filepath.Join(t.TempDir(), "gone.jsonl")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newEndRecorder(t)
			root := initGitRepo(t)
			wtDir := addWorktree(t, root, "WL-2", "no-transcript")

			runHook(t, "session-end", Payload{Cwd: wtDir, SessionID: "sess-1", TranscriptPath: tc.path})
			body := rec.only(t)
			if got := string(body["usage"]); got != "null" {
				t.Fatalf("usage = %s, want null (nil must leave stored usage alone)", got)
			}
		})
	}
}
