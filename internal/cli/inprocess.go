package cli

import (
	"errors"
	"fmt"
)

// The errors of an in-process run of the command tree (WL-REQ-1862,
// WL-REQ-1863). internal/cmd returns them and re-exports them under its own
// names; internal/mcp's lode tool answers them. They live here, in the one
// package both import, because internal/mcp cannot import internal/cmd:
// internal/cmd's tests import internal/api, which imports internal/mcp.

// ErrProjectRequired matches (errors.Is) the error of a command that needs a
// project when none is set.
var ErrProjectRequired = errors.New("no project")

// ErrProjectNotFound matches (errors.Is) the error of a run whose project
// names no project the caller can see.
var ErrProjectNotFound = errors.New("project not found")

// ErrLocalPath matches (errors.Is) the error of an in-process run whose file
// flag names a path instead of "-": the only disk it could read is the
// server's.
var ErrLocalPath = errors.New("an in-process run reads only stdin (-), not a path")

// ErrProjectMismatch matches (errors.Is) the error of an in-process run whose
// --project or --repo names a project other than the run's own.
var ErrProjectMismatch = errors.New("project mismatch")

// ProjectMismatchError is the ErrProjectMismatch a run returns. Requested is
// the flag's value; Current is the run's project, "" when it has none.
type ProjectMismatchError struct {
	Current, Requested string
}

func (e *ProjectMismatchError) Error() string {
	if e.Current == "" {
		return fmt.Sprintf("--project %s: this run has no current project, and a flag cannot choose one", e.Requested)
	}
	return fmt.Sprintf("--project %s names a different project than the current one, %s", e.Requested, e.Current)
}

func (e *ProjectMismatchError) Is(target error) bool { return target == ErrProjectMismatch }

// LocalCommandError is returned when a command that needs the caller's
// machine is asked to run somewhere else (the MCP lode tool).
type LocalCommandError struct {
	// Path is the command path without the root, e.g. "work next".
	Path string
}

func (e *LocalCommandError) Error() string {
	return fmt.Sprintf("refused: lode %s needs the lode binary on the caller's machine", e.Path)
}

// ConfirmationRequiredError is the error of an in-process run whose command
// asks before acting on a terminal and whose stdin did not answer yes.
type ConfirmationRequiredError struct {
	// Command is the command path without the root, e.g. "doc transfer --from".
	Command string
	// Action says what would have happened.
	Action string
}

func (e *ConfirmationRequiredError) Error() string {
	return fmt.Sprintf("lode %s asks before it acts: it would %s. Confirm with the user, then run it again with \"yes\" on stdin", e.Command, e.Action)
}
