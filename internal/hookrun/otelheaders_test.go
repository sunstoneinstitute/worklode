package hookrun

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// otel-headers takes no payload and needs no worktree binding: it answers
// from wherever Claude Code happens to run it, main checkout included.
func TestOTelHeadersPrintsBearerTokenWhenSet(t *testing.T) {
	t.Setenv("LODE_TOKEN", "wl_test_token")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event: "otel-headers", Stdin: strings.NewReader(""),
		Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	want := `{"Authorization":"Bearer wl_test_token"}` + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

// No LODE_TOKEN and no config file: the helper still exits 0 and prints an
// empty object, so Claude Code's export runs unauthenticated rather than
// failing.
func TestOTelHeadersPrintsEmptyObjectWhenNoTokenFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event: "otel-headers", Stdin: strings.NewReader(""),
		Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if want := "{}\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

// otel-headers is answered without a bound worktree -- unlike every other
// event, it must not NOP just because the invoking directory carries no
// lease.
func TestOTelHeadersIgnoresTheWorktreeGuard(t *testing.T) {
	t.Setenv("LODE_TOKEN", "wl_test_token")
	root := initGitRepo(t) // a plain repo, not a task worktree

	stdout, stderr := runHookOutput(t, "otel-headers", Payload{Cwd: root})
	if !strings.Contains(stdout, "Bearer wl_test_token") {
		t.Fatalf("stdout = %q, stderr = %q, want the token despite no worktree binding", stdout, stderr)
	}
}

// otel-headers must never read opts.Stdin: Claude Code invokes this helper
// with no payload, and if it inherits or leaves open a pipe that never
// reaches EOF, an io.ReadAll on it would hang the process forever. Feed the
// read end of a pipe with nothing written (and never closed) and require Run
// to return well before it could possibly have drained it.
func TestOTelHeadersDoesNotBlockOnAnOpenStdinPipe(t *testing.T) {
	t.Setenv("LODE_TOKEN", "wl_test_token")
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Event: "otel-headers", Stdin: pr,
			Stdout: &stdout, Stderr: &stderr,
		})
	}()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
		}
		want := `{"Authorization":"Bearer wl_test_token"}` + "\n"
		if stdout.String() != want {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked reading otel-headers' stdin instead of ignoring it")
	}
}
