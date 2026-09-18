package hookrun

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
