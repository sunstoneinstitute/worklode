//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func runChild(child *exec.Cmd) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	if err := child.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-signals:
			_ = child.Process.Signal(sig)
		case <-done:
		}
	}()
	err := child.Wait()
	close(done)
	return err
}
