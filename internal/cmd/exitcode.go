package cmd

import "os/exec"

// ExitCode returns the exit status of a child process error.
func ExitCode(err error) (int, bool) {
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return 0, false
	}
	return childExitCode(exitErr), true
}
