package cmd

import "os/exec"

// ExitCode returns the exit status of a child process error.
func ExitCode(err error) (int, bool) {
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return 0, false
	}
	if code := exitErr.ExitCode(); code >= 0 {
		return code, true
	}
	// os/exec reports a signal-terminated child as -1 on Unix. 130 is the
	// portable shell status for an interrupt, including CommandContext cancel.
	return 130, true
}
