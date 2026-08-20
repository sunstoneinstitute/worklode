// Package gitexec is the one place worklode shells out to git.
//
// Every git invocation in the binary goes through here so that policy —
// environment hardening, argv shape, error wrapping, and any future timeout
// or `-c` override — is set once instead of in each package that happens to
// need a subprocess. Callers pick the result shape they want (Text, Line,
// Bytes, OK, Run) or build the command themselves with Cmd when they need to
// feed it stdin.
package gitexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// policyEnv is applied to every git subprocess.
//
//   - GIT_OPTIONAL_LOCKS=0 keeps read-mostly commands (status, rev-parse)
//     from taking the index lock to write back a refreshed index, so worklode
//     never contends with an agent's own git in the same worktree. It leaves
//     the locks a writing command genuinely needs alone.
//   - GIT_TERMINAL_PROMPT=0 makes a command that wants credentials fail
//     instead of blocking on a prompt nothing is attached to.
var policyEnv = []string{
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_TERMINAL_PROMPT=0",
}

// Cmd builds a git command that runs in dir, under worklode's standard
// environment. An empty dir runs git in the process's own working directory.
// Use it directly when the invocation needs stdin or a non-standard result
// shape; prefer the helpers below otherwise.
func Cmd(dir string, args ...string) *exec.Cmd {
	return CmdContext(context.Background(), dir, args...)
}

// CmdContext is Cmd bound to ctx, for callers that must not hang — a git
// stuck on a dead network mount holding the worktree, say.
func CmdContext(ctx context.Context, dir string, args ...string) *exec.Cmd {
	argv := args
	if dir != "" {
		argv = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", argv...) //nolint:gosec // fixed program; callers pass fixed argv with server- or git-issued values
	cmd.Env = append(os.Environ(), policyEnv...)
	return cmd
}

// Error is a failed git invocation: the argv it ran, what git wrote to the
// error stream, and the underlying exec error. Its message leads with the
// command so a wrapped error still says which git call failed.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := "git " + strings.Join(e.Args, " ")
	if e.Stderr != "" {
		return msg + ": " + e.Stderr
	}
	return msg + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode reports the exit status git terminated with, or -1 when the
// failure was not an exit status at all (git missing, context cancelled).
// Some exit codes are meaningful: `git config --unset` exits 5 for a key
// that does not exist, which callers read as "already absent".
func ExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// wrap builds an *Error for a failed run, preferring git's own stderr — from
// an ExitError's captured stream, or from the combined output a Run-style
// call already holds — over the bare "exit status N".
func wrap(cmd *exec.Cmd, output []byte, err error) error {
	e := &Error{Args: cmd.Args[1:], Err: err}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		e.Stderr = strings.TrimSpace(string(exitErr.Stderr))
	} else if len(output) > 0 {
		e.Stderr = strings.TrimSpace(string(output))
	}
	return e
}

// Bytes runs git and returns its raw stdout, for callers that need the bytes
// unmodified. A failure comes back as *Error.
func Bytes(dir string, args ...string) ([]byte, error) {
	cmd := Cmd(dir, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, wrap(cmd, nil, err)
	}
	return out, nil
}

// Text runs git and returns its stdout with surrounding whitespace trimmed —
// the shape almost every plumbing command's caller wants. A failure comes
// back as *Error.
func Text(dir string, args ...string) (string, error) {
	out, err := Bytes(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Line runs git and returns its trimmed stdout, with ok=false on any failure
// *or* on empty output: every caller of this shape wants a name, a path or a
// sha, and a command that printed nothing did not answer the question.
func Line(dir string, args ...string) (string, bool) {
	line, err := Text(dir, args...)
	if err != nil || line == "" {
		return "", false
	}
	return line, true
}

// OK runs git for its exit status alone, discarding all output.
func OK(dir string, args ...string) bool {
	return Cmd(dir, args...).Run() == nil
}

// Run runs git for effect, folding its combined output into the error so a
// porcelain command's diagnostics — which it writes to stdout as readily as
// stderr — survive into the message.
func Run(dir string, args ...string) error {
	cmd := Cmd(dir, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return wrap(cmd, out, err)
	}
	return nil
}
