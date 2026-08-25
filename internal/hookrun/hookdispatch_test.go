package hookrun

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardNOPForEveryEvent(t *testing.T) {
	for _, event := range allEvents {
		t.Run(event, func(t *testing.T) {
			rec := newRecordingServer(t)
			root := initGitRepo(t) // plain repo, no worktree dir

			runHook(t, event, Payload{Cwd: root, SessionID: "s1"})
			if rec.hit() {
				t.Fatalf("backbone was called during a guard-NOP: %v", rec.list())
			}
		})
	}
}

// Every listed event must reach a handler: `lode hook --list` advertises this
// set, so an event that only dispatch's default branch answers would be a
// documented no-op. Run outside any repo, where every handler is a guard NOP
// and the only possible output is the unknown-event warning.
func TestEventsAreDispatched(t *testing.T) {
	newRecordingServer(t)
	run := func(event string) string {
		_, stderr := runHookOutput(t, event, Payload{Cwd: t.TempDir()})
		return stderr
	}
	for _, event := range allEvents {
		if got := run(event); strings.Contains(got, "unknown hook event") {
			t.Errorf("listed event %q is not dispatched: %s", event, got)
		}
	}
	if got := run("not-an-event"); !strings.Contains(got, "unknown hook event") {
		t.Errorf("unlisted event: stderr = %q, want an unknown-event warning", got)
	}
}

func TestNextChainRunsDownstreamOnGuardNOP(t *testing.T) {
	newRecordingServer(t)
	root := initGitRepo(t)
	marker := filepath.Join(t.TempDir(), "downstream-ran")
	payload := payloadJSON(t, Payload{Cwd: root, SessionID: "s1"})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "pre-commit",
		Next:   []string{"touch", marker},
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("downstream did not run, marker %s missing: %v", marker, err)
	}
}

func TestNextChainReceivesPayloadOnStdin(t *testing.T) {
	newRecordingServer(t)
	root := initGitRepo(t)
	sink := filepath.Join(t.TempDir(), "downstream-stdin")
	payload := payloadJSON(t, Payload{Cwd: root, SessionID: "chain-session"})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "session-end",
		Next:   []string{"sh", "-c", "cat > " + sink},
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("read downstream stdin capture: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downstream stdin = %q, want the original payload %q", got, payload)
	}
}

func TestNextChainPropagatesExitCode(t *testing.T) {
	newRecordingServer(t)
	root := initGitRepo(t)
	payload := payloadJSON(t, Payload{Cwd: root})

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "pre-commit",
		Next:   []string{"sh", "-c", "exit 7"},
		Stdin:  bytes.NewReader(payload),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 7 {
		t.Fatalf("exit code = %d, want 7 (child's code)", code)
	}
}
