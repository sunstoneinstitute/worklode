package cmd

import "os/exec"

func childExitCode(err *exec.ExitError) int {
	return err.ExitCode()
}
