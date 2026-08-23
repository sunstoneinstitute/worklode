//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"os/exec"
	"syscall"
)

func childExitCode(err *exec.ExitError) int {
	if code := err.ExitCode(); code >= 0 {
		return code
	}
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return -1
}
