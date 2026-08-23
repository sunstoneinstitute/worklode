package cmd

import "os/exec"

func runChild(child *exec.Cmd) error {
	return child.Run()
}
