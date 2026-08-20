package gitexec_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// initRepo makes an empty repo with one commit and returns its root.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "root"},
	} {
		if out, err := gitexec.Cmd(root, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return root
}

// TestTextTrimsAndTargetsDir: Text runs in the directory it is given and
// hands back stdout without the trailing newline git writes.
func TestTextTrimsAndTargetsDir(t *testing.T) {
	root := initRepo(t)
	got, err := gitexec.Text(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if got != "main" {
		t.Errorf("Text = %q, want %q", got, "main")
	}
}

// TestBytesKeepsRawOutput: Bytes does not trim, so a caller parsing
// newline-delimited output sees exactly what git wrote.
func TestBytesKeepsRawOutput(t *testing.T) {
	root := initRepo(t)
	out, err := gitexec.Bytes(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(out) != "main\n" {
		t.Errorf("Bytes = %q, want %q", out, "main\n")
	}
}

// TestErrorCarriesStderrAndArgv: a failing command reports what git said and
// which command said it, not a bare "exit status 128".
func TestErrorCarriesStderrAndArgv(t *testing.T) {
	root := initRepo(t)
	_, err := gitexec.Text(root, "rev-parse", "definitely-not-a-ref")
	if err == nil {
		t.Fatal("Text on a missing ref: want error, got nil")
	}
	var gitErr *gitexec.Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("error is %T, want *gitexec.Error", err)
	}
	if gitErr.Stderr == "" {
		t.Errorf("Error.Stderr is empty, want git's message; got %v", err)
	}
	if !strings.Contains(err.Error(), "definitely-not-a-ref") {
		t.Errorf("Error() = %q, want it to name the failing argv", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("Error does not unwrap to *exec.ExitError: %v", err)
	}
}

// TestExitCodeReportsGitStatus: ExitCode surfaces the status callers switch
// on — `config --unset` of an absent key exits 5, which means "already gone".
func TestExitCodeReportsGitStatus(t *testing.T) {
	root := initRepo(t)
	err := gitexec.Run(root, "config", "--unset", "worklode.nothing-here")
	if err == nil {
		t.Fatal("unsetting an absent key: want error, got nil")
	}
	if code := gitexec.ExitCode(err); code != 5 {
		t.Errorf("ExitCode = %d, want 5", code)
	}
	if code := gitexec.ExitCode(errors.New("not an exec failure")); code != -1 {
		t.Errorf("ExitCode of a non-exec error = %d, want -1", code)
	}
}

// TestLineRejectsEmptyOutput: a command that succeeds but prints nothing is
// not an answer, so Line reports it the same as a failure.
func TestLineRejectsEmptyOutput(t *testing.T) {
	root := initRepo(t)
	if _, ok := gitexec.Line(root, "status", "--porcelain"); ok {
		t.Error("Line on a clean tree: want ok=false for empty output")
	}
	if line, ok := gitexec.Line(root, "rev-parse", "--abbrev-ref", "HEAD"); !ok || line != "main" {
		t.Errorf("Line = %q, %v; want %q, true", line, ok, "main")
	}
	if _, ok := gitexec.Line(t.TempDir(), "rev-parse", "--show-toplevel"); ok {
		t.Error("Line outside a repo: want ok=false")
	}
}

// TestOKReportsExitStatusOnly: OK ignores output entirely.
func TestOKReportsExitStatusOnly(t *testing.T) {
	root := initRepo(t)
	if !gitexec.OK(root, "rev-parse", "--verify", "HEAD") {
		t.Error("OK on an existing ref = false, want true")
	}
	if gitexec.OK(root, "rev-parse", "--verify", "definitely-not-a-ref") {
		t.Error("OK on a missing ref = true, want false")
	}
}

// TestRunFoldsCombinedOutput: porcelain commands write diagnostics to stdout
// as readily as stderr, so Run's error must carry both.
func TestRunFoldsCombinedOutput(t *testing.T) {
	root := initRepo(t)
	if err := gitexec.Run(root, "config", "worklode.set-by-run", "yes"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, err := gitexec.Text(root, "config", "--get", "worklode.set-by-run"); err != nil || got != "yes" {
		t.Fatalf("config --get = %q, %v; want %q", got, err, "yes")
	}
	err := gitexec.Run(root, "worktree", "add", filepath.Join(root, "wt"), "no-such-branch")
	if err == nil {
		t.Fatal("worktree add of a missing branch: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("Run error = %q, want git's own diagnostic", err)
	}
}

// TestCmdAcceptsStdin: the builder is the escape hatch for invocations that
// pipe input, and they get the same policy environment as the helpers.
func TestCmdAcceptsStdin(t *testing.T) {
	root := initRepo(t)
	cmd := gitexec.Cmd(root, "stripspace", "--strip-comments")
	cmd.Stdin = strings.NewReader("# a comment\nbody\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("stripspace: %v", err)
	}
	if string(out) != "body\n" {
		t.Errorf("stripspace = %q, want %q", out, "body\n")
	}
}

// TestPolicyEnvIsApplied: every invocation carries worklode's git policy, and
// inherits the process environment rather than replacing it.
func TestPolicyEnvIsApplied(t *testing.T) {
	t.Setenv("WORKLODE_GITEXEC_PROBE", "inherited")
	cmd := gitexec.Cmd("", "version")
	env := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "WORKLODE_GITEXEC_PROBE=inherited"} {
		if !strings.Contains(env, want) {
			t.Errorf("Cmd env is missing %s", want)
		}
	}
}

// TestEmptyDirRunsInProcessCwd: an empty dir means "wherever the process is",
// with no -C in the argv at all.
func TestEmptyDirRunsInProcessCwd(t *testing.T) {
	cmd := gitexec.Cmd("", "status")
	if got, want := strings.Join(cmd.Args[1:], " "), "status"; got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if got, want := strings.Join(gitexec.Cmd("/tmp", "status").Args[1:], " "), "-C /tmp status"; got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// TestCmdContextCancels: a caller that must not hang can bound the call.
func TestCmdContextCancels(t *testing.T) {
	root := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := gitexec.CmdContext(ctx, root, "status").Run()
	if err == nil {
		t.Fatal("cancelled context: want error, got nil")
	}
}

// TestBytesOutsideRepo: the common "not a git repository" case is an error,
// not an empty success, wherever a caller reads it from.
func TestBytesOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Skip("temp dir is inside a repo")
	}
	if _, err := gitexec.Bytes(dir, "rev-parse", "--absolute-git-dir"); err == nil {
		t.Error("rev-parse outside a repo: want error, got nil")
	}
}
